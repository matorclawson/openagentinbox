# Rasa action server

Install the repository's integrations/python package alongside your compatible rasa-sdk. Set OPENAGENTINBOX_PROFILE to the action worker's private seat profile and OPENAGENTINBOX_REVIEW_INBOX to the authorized human inbox. From this folder run `python -m rasa_sdk --actions actions`. Merge the domain and endpoints fragments into your assistant. For CALM add an action step to your confirmed human-review flow:

    flows:
      request_review:
        description: Let the user request a review by the support team
        steps:
          - action: action_request_human_review

For a rules-based assistant add training examples for the request_human_review intent and merge rules.yml. Exercise the action from a test channel that supplies a stable incoming message_id. Replaying the same turn should leave exactly one message in the configured inbox. Both sides receive the same opaque reference. The mailbox is the action server’s service identity. For systems serving multiple tenants, configure separate isolated action workers or resolve credentials only from a trusted tenant mapping.
