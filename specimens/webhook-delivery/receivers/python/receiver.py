import base64
import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import ClassVar

from standardwebhooks import Webhook


def require_environment(name: str) -> str:
    value = os.environ.get(name)
    if value:
        return value

    raise RuntimeError(f"{name} is required")


def parse_address(value: str) -> tuple[str, int]:
    host, separator, port_text = value.rpartition(":")
    if not separator or not host:
        raise RuntimeError("RECEIVER_ADDR must be a loopback host:port")
    if host not in {"127.0.0.1", "localhost"}:
        raise RuntimeError("RECEIVER_ADDR must use a loopback host")

    try:
        port = int(port_text)
    except ValueError as error:
        raise RuntimeError("RECEIVER_ADDR must contain a valid port") from error

    if port < 1 or port > 65_535:
        raise RuntimeError("RECEIVER_ADDR must contain a valid port")

    return host, port


def tamper_versioned_signature(versioned_signature: str) -> str:
    prefix, separator, encoded = versioned_signature.partition(",")
    if not separator or not encoded:
        return versioned_signature

    replacement = "A"
    if encoded[0] == "A":
        replacement = "B"

    return f"{prefix},{replacement}{encoded[1:]}"


def tamper_signature(signature: str) -> str:
    return " ".join(
        tamper_versioned_signature(versioned_signature)
        for versioned_signature in signature.split(" ")
    )


def verify_receipt(
    verifier: Webhook,
    body: bytes,
    headers: dict[str, str],
) -> dict[str, object]:
    payload: object = None
    accepted = False
    try:
        payload = verifier.verify(body, headers)
        accepted = True
    except Exception:
        pass

    tampered_headers = dict(headers)
    tampered_headers["webhook-signature"] = tamper_signature(
        headers["webhook-signature"]
    )
    tampered_rejected = False
    try:
        verifier.verify(body, tampered_headers)
    except Exception:
        tampered_rejected = True

    return {
        "messageId": headers["webhook-id"],
        "timestamp": headers["webhook-timestamp"],
        "payload": payload,
        "payloadBase64": base64.b64encode(body).decode("ascii"),
        "accepted": accepted,
        "tamperedRejected": tampered_rejected,
    }


def append_receipt(path: str, receipt: dict[str, object]) -> None:
    with open(path, "a", encoding="utf-8") as result:
        encoded = json.dumps(receipt, separators=(",", ":"))
        result.write(f"{encoded}\n")


class ReceiverHandler(BaseHTTPRequestHandler):
    verifier: ClassVar[Webhook]
    result_path: ClassVar[str]

    def do_GET(self) -> None:
        if self.path != "/health":
            self.respond(404)
            return

        self.respond(204)

    def do_POST(self) -> None:
        if self.path != "/webhook":
            self.respond(404)
            return

        length_text = self.headers.get("content-length")
        if not length_text:
            self.respond(411)
            return

        try:
            length = int(length_text)
        except ValueError:
            self.respond(400)
            return

        if length < 0:
            self.respond(400)
            return

        body = self.rfile.read(length)
        headers = {
            "webhook-id": self.headers.get("webhook-id", ""),
            "webhook-timestamp": self.headers.get("webhook-timestamp", ""),
            "webhook-signature": self.headers.get("webhook-signature", ""),
        }
        receipt = verify_receipt(self.verifier, body, headers)
        append_receipt(self.result_path, receipt)

        if not receipt["accepted"]:
            self.respond(401)
            return
        if not receipt["tamperedRejected"]:
            self.respond(500)
            return

        self.respond(204)

    def respond(self, status: int) -> None:
        self.send_response(status)
        self.send_header("content-length", "0")
        self.end_headers()

    def log_message(self, format: str, *args: object) -> None:
        return


def main() -> None:
    address = parse_address(require_environment("RECEIVER_ADDR"))
    ReceiverHandler.verifier = Webhook(require_environment("RECEIVER_SECRET"))
    ReceiverHandler.result_path = require_environment("RECEIVER_RESULT")

    server = HTTPServer(address, ReceiverHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
