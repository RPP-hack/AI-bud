"""Confirmed Jira read-access requests for the first-task demo."""

from datetime import date

from .storage import Audience

# Demo identities stand in for the authenticated session from the hosting backend.
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
    with store.connect(read_only=not confirm) as db:
        db.execute("BEGIN IMMEDIATE" if confirm else "BEGIN")
        # A changed audience, retired guide or unresolved rule disables submission,
        # even if the caller kept a draft from before the last index update.
        rows = db.execute("""
            SELECT document_id, rule_value FROM documents d
            WHERE current = 1 AND (team = '*' OR team = ?) AND rule_key = 'jira.bud.read'
              AND (review_by IS NULL OR review_by >= ?)
              AND EXISTS (SELECT 1 FROM json_each(d.roles) WHERE value = ?)
        """, (audience.team, date.today().isoformat(), audience.role)).fetchall()
        if (not any(row["document_id"] == "jira-access" for row in rows)
                or {row["rule_value"] for row in rows} != {"Подать заявку на чтение BUD"}):
            raise PermissionError("Нет согласованной действующей инструкции. Уточни шаг у наставника.")
        result = dict(JIRA_REQUEST, status="draft", next_step="Проверь поля и подтверди отправку.")
        if confirm:
            db.execute("""
                INSERT INTO access_requests(employee, project, permission, reason, status)
                VALUES(:employee, :project, :permission, :reason, 'pending')
                ON CONFLICT(employee, project, permission) DO NOTHING
            """, dict(employee=employee, **JIRA_REQUEST))
        row = db.execute("""
            SELECT id, status, reason FROM access_requests
            WHERE employee = ? AND project = ? AND permission = ?
        """, (employee, JIRA_REQUEST["project"], JIRA_REQUEST["permission"])).fetchone()
        if row is None:
            return result
        return dict(result, request_id=f"BUD-ACCESS-{row['id']}", status=row["status"],
                    reason=row["reason"], next_step="На согласовании. Дождись решения владельца проекта.")
