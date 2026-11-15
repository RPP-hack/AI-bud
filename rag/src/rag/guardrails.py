"""Deterministic boundaries, not a semantic verifier or a complete injection detector."""

import re
import unicodedata

POLICY_VERSION = "1"
MAX_STEPS = 32
MAX_STEP_CHARS = 2000
MAX_ANSWER_CHARS = 16000


def validate_question(question):
    if (not isinstance(question, str) or not question.strip() or len(question) > 4000
            or any(unicodedata.category(c) == "Cs" or (
                unicodedata.category(c) == "Cc" and c not in "\n\r\t")
                   for c in question)):
        raise ValueError("Supply a question between 1 and 4000 characters without control characters")


def _normalized(text):
    # Detect common invisible-character / compatibility-character obfuscations.
    return "".join(c for c in unicodedata.normalize("NFKC", text)
                   if unicodedata.category(c) != "Cf").casefold().replace("ё", "е")


_INJECTION = re.compile(
    r"(?:ignore|disregard|override)\s+(?:(?:all|the|any)\s+)?"
    r"(?:previous|prior|system|developer|above)\s+(?:instructions?|prompts?|rules?)"
    r"|(?:игнориру\w*|забудь|отмени)\s+(?:все\s+)?"
    r"(?:предыдущ\w*|системн\w*|вышестоящ\w*)\s+(?:инструкц\w*|правил\w*|промпт\w*)"
    r"|(?:reveal|print|show|repeat)\s+(?:the\s+|your\s+)?(?:system|developer)\s+prompt"
    r"|(?:покажи|раскрой|выведи|повтори)\s+(?:свой\s+)?системн\w*\s+промпт\w*"
    r"|<\|(?:im_start|im_end|system|assistant|endoftext)\|>"
    r"|\[/?inst\]|<<\s*/?sys\s*>>",
)


def is_instruction_attack(text):
    return bool(_INJECTION.search(_normalized(text)))


# These conservative patterns catch explicit completion claims. Arbitrary paraphrases
# still require semantic evaluation; the action handler remains the authority.
_COMPLETED_ACTION = re.compile(
    r"\b(?:заявк\w*|запрос|тикет)\s+(?:\w+\s+){0,3}"
    r"(?:отправлен\w*|создан\w*|подан\w*|оформлен\w*|одобрен\w*|зарегистрирован\w*)"
    r"|\bдоступ\s+(?:\w+\s+){0,3}(?:выдан\w*|предоставлен\w*|открыт\w*|разрешен\w*)"
    r"|\bя\s+(?:уже\s+)?(?:отправил\w*|создал\w*|оформил\w*|выдал\w*|подал\w*)"
    r"|\bi\s+(?:have\s+)?(?:already\s+)?(?:submitted|created|sent|granted|approved)\b"
    r"|\b(?:request|ticket|access)\s+(?:\w+\s+){0,3}"
    r"(?:submitted|created|sent|granted|approved)\b"
    r"|\bbud-access-\d+\b",
)
_URL = re.compile(r"https?://[^\s<>\[\]()\"']+", re.IGNORECASE)


def output_violation(text, cited_sources):
    """Return a safe reason code; never put provider content in exceptions/logs."""
    if (len(text) > MAX_STEP_CHARS or any(
        unicodedata.category(c) in ("Cs", "Cf") or (
            unicodedata.category(c) == "Cc" and c not in "\n\r\t") for c in text
    )):
        return "invalid_output_text"
    if is_instruction_attack(text):
        return "instruction_attack"
    for match in _COMPLETED_ACTION.finditer(_normalized(text)):
        # Future / negated statements describe the process, not a receipt.
        if not re.search(r"\b(?:не|нет|будет|будут|может|должна|должен|not|will|would|can|should)\b",
                         match.group()):
            return "unverified_action"
    # Sources already have structured URLs; generated markup must not add trackers
    # or active content. Clients should still render answer text as plain text.
    if re.search(r"<\s*/?\s*[a-zA-Z][^>]*>|!\[|\]\s*\(|javascript\s*:|data\s*:", text, re.IGNORECASE):
        return "active_content"
    allowed = set()
    for source in cited_sources:
        allowed.update(url.rstrip(".,;!?") for url in _URL.findall(source.text))
        if source.source_url:
            allowed.add(source.source_url)
    if any(url.rstrip(".,;!?") not in allowed for url in _URL.findall(text)):
        return "untrusted_link"
    return None
