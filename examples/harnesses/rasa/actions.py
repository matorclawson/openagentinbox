"""Register on the trusted Rasa action server. Never relay mailbox contents to chat."""
import asyncio
import json
import os
from uuid import NAMESPACE_URL, uuid5

from openagentinbox import Inbox
from rasa_sdk import Action


class ActionRequestHumanReview(Action):
    def name(self):
        return "action_request_human_review"

    async def run(self, dispatcher, tracker, domain):
        incoming = tracker.latest_message.get("message_id")
        if not incoming:
            dispatcher.utter_message(text="I need a message ID to submit this request.")
            return []
        # Stable across retries of the same user turn, scoped to this assistant's seat.
        mailbox = Inbox.from_env()
        identity = await asyncio.to_thread(mailbox.whoami)
        key = uuid5(NAMESPACE_URL, json.dumps([identity["mailbox_id"], tracker.sender_id, incoming, self.name()]))
        await asyncio.to_thread(mailbox.send, os.environ["OPENAGENTINBOX_REVIEW_INBOX"],
                                "Human review requested", f"Support conversation reference: {key}", str(key))
        dispatcher.utter_message(text=f"Your review request was sent. Reference: {key}")
        return []
