import re

from .guardrails import validate_question
from .terms import search_terms


def validate_history(history):
    if history is None:
        return []
    if not isinstance(history, (list, tuple)) or len(history) > 6:
        raise ValueError("Invalid conversation history")
    result = []
    size = 0
    for item in history:
        if (not isinstance(item, dict) or set(item) != {"role", "content"}
                or item["role"] not in ("user", "assistant")):
            raise ValueError("Invalid conversation message")
        validate_question(item["content"])
        size += len(item["content"])
        if size > 8000:
            raise ValueError("Conversation history is too large")
        result.append(dict(item))
    return result


def retrieval_question(question, history):
    if set(search_terms(question)) & {"jira", "403", "bud", "ревью", "bitbucket", "pr", "dev", "окружение"}:
        return question
    if not re.search(r"^(?:а\b|и\b|к какому|какой|какая|какие|что дальше|что потом|это|он\b|она\b|не помогло|почему|как это)", question.strip(), re.IGNORECASE):
        return question
    previous = [item["content"] for item in history if item["role"] == "user"]
    if not previous:
        return question
    for content in reversed(previous):
        if set(search_terms(content)) & {"jira", "403", "bud", "ревью", "bitbucket", "pr", "dev", "окружение"}:
            return content
    return previous[-1]
