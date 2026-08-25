import json
import os

from openfeature import api
from openfeature.contrib.provider.ofrep import OFREPProvider
from openfeature.evaluation_context import EvaluationContext


token = os.environ.get("REAPER_EVALUATION_TOKEN")
if not token:
    raise RuntimeError("REAPER_EVALUATION_TOKEN is required")

endpoint = os.environ.get(
    "OFREP_ENDPOINT",
    "http://127.0.0.1:8080/environments/production",
)
provider = OFREPProvider(
    endpoint,
    headers_factory=lambda: {"Authorization": f"Bearer {token}"},
)
api.set_provider_and_wait(provider)

client = api.get_client("reaper-python-example")
details = client.get_boolean_details(
    "checkout-v2",
    False,
    EvaluationContext(
        targeting_key=os.environ.get("TARGETING_KEY", "user-2"),
        attributes={"organization.id": os.environ.get("ORGANIZATION_ID", "acme")},
    ),
)

print(
    json.dumps(
        {
            "language": "python",
            "value": details.value,
            "variant": details.variant,
            "reason": details.reason.name,
        },
        separators=(",", ":"),
    )
)

api.shutdown()
