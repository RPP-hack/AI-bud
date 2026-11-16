import re

from .generation import GeneratedClaim, GenerationResult


def conversational_reply(question, audience):
    text = re.sub(r"[^\w\s]", " ", question.casefold().replace("ё", "е"))
    text = " ".join(text.split())
    text = re.sub(r"^(?:привет|здравствуй|здравствуйте|добрый день|доброе утро|добрый вечер|hello|hi)\s*", "", text).strip()
    if not text or re.fullmatch(r"(?:кто ты|что ты умеешь|чем (?:ты )?можешь помочь|что (?:я )?могу (?:тут|здесь) найти|что (?:тут|здесь) (?:есть|можно найти)|расскажи о себе|помоги|помощь)", text):
        team = "твоей команды"
        return GenerationResult("conversation", "rules", (
            GeneratedClaim("Привет! Я AI-buddy, твой помощник по онбордингу. Помогу найти доступные тебе инструкции и разобраться со следующим шагом.", ()),
            GeneratedClaim(f"Можно спросить про доступы, первый pull request, ревью или чек-лист первых недель {team}.", ()),
        ), next_step="Опиши, что хочешь сделать или какая ошибка мешает. Например: «Не открывается Jira, ошибка 403».")
    if re.fullmatch(r"(?:спасибо(?: большое)?|благодарю|спасибо помогло|все (?:понятно|получилось)|помогло|вопрос решен|пока)", text):
        return GenerationResult("conversation", "rules", (GeneratedClaim("Рад помочь! Если появится следующий вопрос по работе или онбордингу, напиши.", ()),))
    if re.fullmatch(r"(?:не работает|ничего не работает|не получается|у меня (?:проблема|ошибка)|помоги разобраться|что делать|есть вопрос)", text):
        return GenerationResult("conversation", "rules", (GeneratedClaim("Что именно не получается: открыть Jira, получить доступ, запустить проект или отправить pull request? Напиши название инструмента, текст ошибки и что уже пробовал.", ()),))
    return None
