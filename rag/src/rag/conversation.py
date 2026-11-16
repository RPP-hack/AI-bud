from .guardrails import validate_question


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
    previous = [item["content"] for item in history if item["role"] == "user"]
    if not previous:
        return question
    context = "\n".join(previous[-2:])
    budget = max(0, 4000 - len(question) - len("\nКонтекст: "))
    if not budget:
        return question
    return question + "\nКонтекст: " + context[-min(2000, budget):]
