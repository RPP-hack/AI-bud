"""Explicit protobuf mappings; REST keeps the existing lowercase JSON contract."""

from rag.v1 import rag_pb2 as pb


STATUSES = {
    "answered": pb.ANSWER_STATUS_ANSWERED,
    "sources_found": pb.ANSWER_STATUS_SOURCES_FOUND,
    "insufficient_evidence": pb.ANSWER_STATUS_INSUFFICIENT_EVIDENCE,
    "conflict": pb.ANSWER_STATUS_CONFLICT,
    "needs_review": pb.ANSWER_STATUS_NEEDS_REVIEW,
}


def source_message(source):
    return pb.Source(**{key: value for key, value in source.items() if value is not None})


def answer_message(result):
    response = pb.AnswerResponse(request_id=result["request_id"], status=STATUSES[result["status"]],
                                 mode=result["mode"], steps=[pb.Step(**s) for s in result["steps"]],
                                 sources={key: source_message(s) for key, s in result["sources"].items()},
                                 next_step=result["next_step"])
    for name in ("model", "action"):
        if result[name] is not None:
            setattr(response, name, result[name])
    return response
