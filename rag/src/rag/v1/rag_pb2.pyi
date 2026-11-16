from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AnswerMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ANSWER_MODE_UNSPECIFIED: _ClassVar[AnswerMode]
    ANSWER_MODE_LLM: _ClassVar[AnswerMode]
    ANSWER_MODE_OFFLINE: _ClassVar[AnswerMode]

class AnswerStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ANSWER_STATUS_UNSPECIFIED: _ClassVar[AnswerStatus]
    ANSWER_STATUS_ANSWERED: _ClassVar[AnswerStatus]
    ANSWER_STATUS_SOURCES_FOUND: _ClassVar[AnswerStatus]
    ANSWER_STATUS_INSUFFICIENT_EVIDENCE: _ClassVar[AnswerStatus]
    ANSWER_STATUS_CONFLICT: _ClassVar[AnswerStatus]
    ANSWER_STATUS_NEEDS_REVIEW: _ClassVar[AnswerStatus]
    ANSWER_STATUS_CONVERSATION: _ClassVar[AnswerStatus]
ANSWER_MODE_UNSPECIFIED: AnswerMode
ANSWER_MODE_LLM: AnswerMode
ANSWER_MODE_OFFLINE: AnswerMode
ANSWER_STATUS_UNSPECIFIED: AnswerStatus
ANSWER_STATUS_ANSWERED: AnswerStatus
ANSWER_STATUS_SOURCES_FOUND: AnswerStatus
ANSWER_STATUS_INSUFFICIENT_EVIDENCE: AnswerStatus
ANSWER_STATUS_CONFLICT: AnswerStatus
ANSWER_STATUS_NEEDS_REVIEW: AnswerStatus
ANSWER_STATUS_CONVERSATION: AnswerStatus

class Actor(_message.Message):
    __slots__ = ("user_id", "role", "team")
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    ROLE_FIELD_NUMBER: _ClassVar[int]
    TEAM_FIELD_NUMBER: _ClassVar[int]
    user_id: str
    role: str
    team: str
    def __init__(self, user_id: _Optional[str] = ..., role: _Optional[str] = ..., team: _Optional[str] = ...) -> None: ...

class ConversationMessage(_message.Message):
    __slots__ = ("role", "content")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: str
    def __init__(self, role: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class AnswerRequest(_message.Message):
    __slots__ = ("request_id", "question", "actor", "mode", "top_k", "history")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    QUESTION_FIELD_NUMBER: _ClassVar[int]
    ACTOR_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    HISTORY_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    question: str
    actor: Actor
    mode: AnswerMode
    top_k: int
    history: _containers.RepeatedCompositeFieldContainer[ConversationMessage]
    def __init__(self, request_id: _Optional[str] = ..., question: _Optional[str] = ..., actor: _Optional[_Union[Actor, _Mapping]] = ..., mode: _Optional[_Union[AnswerMode, str]] = ..., top_k: _Optional[int] = ..., history: _Optional[_Iterable[_Union[ConversationMessage, _Mapping]]] = ...) -> None: ...

class SearchRequest(_message.Message):
    __slots__ = ("request_id", "query", "actor", "top_k")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    ACTOR_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    query: str
    actor: Actor
    top_k: int
    def __init__(self, request_id: _Optional[str] = ..., query: _Optional[str] = ..., actor: _Optional[_Union[Actor, _Mapping]] = ..., top_k: _Optional[int] = ...) -> None: ...

class Step(_message.Message):
    __slots__ = ("text", "source_ids")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_IDS_FIELD_NUMBER: _ClassVar[int]
    text: str
    source_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, text: _Optional[str] = ..., source_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class Source(_message.Message):
    __slots__ = ("document_id", "version_id", "chunk_id", "title", "text", "source_url", "page_number", "origin", "stale", "review_by", "start_char", "end_char")
    DOCUMENT_ID_FIELD_NUMBER: _ClassVar[int]
    VERSION_ID_FIELD_NUMBER: _ClassVar[int]
    CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_URL_FIELD_NUMBER: _ClassVar[int]
    PAGE_NUMBER_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    STALE_FIELD_NUMBER: _ClassVar[int]
    REVIEW_BY_FIELD_NUMBER: _ClassVar[int]
    START_CHAR_FIELD_NUMBER: _ClassVar[int]
    END_CHAR_FIELD_NUMBER: _ClassVar[int]
    document_id: str
    version_id: str
    chunk_id: str
    title: str
    text: str
    source_url: str
    page_number: int
    origin: str
    stale: bool
    review_by: str
    start_char: int
    end_char: int
    def __init__(self, document_id: _Optional[str] = ..., version_id: _Optional[str] = ..., chunk_id: _Optional[str] = ..., title: _Optional[str] = ..., text: _Optional[str] = ..., source_url: _Optional[str] = ..., page_number: _Optional[int] = ..., origin: _Optional[str] = ..., stale: _Optional[bool] = ..., review_by: _Optional[str] = ..., start_char: _Optional[int] = ..., end_char: _Optional[int] = ...) -> None: ...

class AnswerResponse(_message.Message):
    __slots__ = ("request_id", "status", "mode", "steps", "sources", "next_step", "model", "action")
    class SourcesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: Source
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[Source, _Mapping]] = ...) -> None: ...
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    STEPS_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    NEXT_STEP_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    status: AnswerStatus
    mode: str
    steps: _containers.RepeatedCompositeFieldContainer[Step]
    sources: _containers.MessageMap[str, Source]
    next_step: str
    model: str
    action: str
    def __init__(self, request_id: _Optional[str] = ..., status: _Optional[_Union[AnswerStatus, str]] = ..., mode: _Optional[str] = ..., steps: _Optional[_Iterable[_Union[Step, _Mapping]]] = ..., sources: _Optional[_Mapping[str, Source]] = ..., next_step: _Optional[str] = ..., model: _Optional[str] = ..., action: _Optional[str] = ...) -> None: ...

class SearchResponse(_message.Message):
    __slots__ = ("request_id", "sources")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    sources: _containers.RepeatedCompositeFieldContainer[Source]
    def __init__(self, request_id: _Optional[str] = ..., sources: _Optional[_Iterable[_Union[Source, _Mapping]]] = ...) -> None: ...
