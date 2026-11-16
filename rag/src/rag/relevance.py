from .terms import search_terms


def focused_passages(query, passages):
    terms = set(search_terms(query))
    if "ревью" in terms:
        topic = "review"
    elif terms & {"jira", "403", "bud"}:
        topic = "jira"
    elif "dev" in terms or "окружение" in terms:
        topic = "dev"
    elif terms & {"bitbucket", "pr", "репозиторий", "клонировать", "ci", "сборка"}:
        topic = "code"
    else:
        if not passages:
            return []
        primary = passages[0]
        return [p for p in passages if p.document_id == primary.document_id or
                (primary.rule_key and p.rule_key == primary.rule_key)]
    result = []
    for passage in passages:
        title = set(search_terms(passage.title))
        if topic == "review":
            matched = "ревью" in title or (passage.rule_key or "").startswith("pr.reviewer")
        elif topic == "jira":
            matched = "jira" in title or (passage.rule_key or "").startswith("jira.")
        elif topic == "dev":
            matched = "dev" in title or "окружение" in title
        else:
            matched = bool(title & {"bitbucket", "pr", "репозиторий", "сборка"}) and not passage.rule_key
        if matched:
            result.append(passage)
    return result
