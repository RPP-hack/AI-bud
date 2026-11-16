"""Confirmed Jira read-access requests for the first-task demo."""

from datetime import date

from .storage import Audience

PROFILES = {
    "intern-platform": Audience("intern", "platform"),
    "mentor-platform": Audience("mentor", "platform"),
    "intern-search": Audience("intern", "search"),
}
JIRA_REQUEST = dict(system="Jira", project="BUD", permission="чтение",
                    reason="Посмотреть первую задачу стажировки")


def request_access(store, employee, *, confirm=False):
    """Preview or submit the one supported, server-authorized demo request."""
    if type(confirm) is not bool:
        raise ValueError("Confirmation must be a boolean")
    audience = PROFILES.get(employee)
    if audience != Audience("intern", "platform"):
        raise PermissionError("Эта операция недоступна для текущего профиля.")
    with store.access_transaction(confirm=confirm) as db:
        rows = store.access_rules(db, audience, date.today())
        if (not any(row["document_id"] == "jira-access" for row in rows)
                or {row["rule_value"] for row in rows} != {"Подать заявку на чтение BUD"}):
            raise PermissionError("Нет согласованной действующей инструкции. Уточни шаг у наставника.")
        result = dict(JIRA_REQUEST, status="draft", next_step="Проверь поля и подтверди отправку.")
        row = store.access_receipt(db, employee, JIRA_REQUEST, confirm=confirm)
        if row is None:
            return result
        return dict(result, request_id=f"BUD-ACCESS-{row['id']}", status=row["status"],
                    reason=row["reason"], next_step="На согласовании. Дождись решения владельца проекта.")
