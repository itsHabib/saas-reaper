import { appendFileSync } from "node:fs";
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";

import { Webhook, WebhookVerificationError } from "standardwebhooks";

interface ReceiverAddress {
  host: string;
  port: number;
}

interface Receipt {
  messageId: string;
  timestamp: string;
  payload: unknown;
  payloadBase64: string;
  accepted: boolean;
  tamperedRejected: boolean;
}

function requireEnvironment(name: string): string {
  const value = process.env[name];
  if (value) {
    return value;
  }

  throw new Error(`${name} is required`);
}

function parseAddress(value: string): ReceiverAddress {
  const separator = value.lastIndexOf(":");
  if (separator <= 0) {
    throw new Error("RECEIVER_ADDR must be a loopback host:port");
  }

  const host = value.slice(0, separator);
  if (host !== "127.0.0.1" && host !== "localhost") {
    throw new Error("RECEIVER_ADDR must use a loopback host");
  }

  const port = Number(value.slice(separator + 1));
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error("RECEIVER_ADDR must contain a valid port");
  }

  return { host, port };
}

function headerValue(request: IncomingMessage, name: string): string {
  const value = request.headers[name];
  if (typeof value === "string") {
    return value;
  }
  if (Array.isArray(value)) {
    return value.join(" ");
  }

  return "";
}

function verificationHeaders(request: IncomingMessage): Record<string, string> {
  return {
    "webhook-id": headerValue(request, "webhook-id"),
    "webhook-timestamp": headerValue(request, "webhook-timestamp"),
    "webhook-signature": headerValue(request, "webhook-signature"),
  };
}

async function readRawBody(request: IncomingMessage): Promise<Buffer> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    if (typeof chunk === "string") {
      chunks.push(Buffer.from(chunk));
      continue;
    }

    chunks.push(chunk as Buffer);
  }

  return Buffer.concat(chunks);
}

function tamperVersionedSignature(versionedSignature: string): string {
  const separator = versionedSignature.indexOf(",");
  if (separator < 0 || separator === versionedSignature.length - 1) {
    return versionedSignature;
  }

  const encoded = versionedSignature.slice(separator + 1);
  let replacement = "A";
  if (encoded[0] === "A") {
    replacement = "B";
  }

  return `${versionedSignature.slice(0, separator + 1)}${replacement}${encoded.slice(1)}`;
}

function tamperSignature(signature: string): string {
  return signature
    .split(" ")
    .map(tamperVersionedSignature)
    .join(" ");
}

function verifyReceipt(
  verifier: Webhook,
  body: Buffer,
  headers: Record<string, string>,
): Receipt {
  let payload: unknown = null;
  let accepted = false;
  try {
    payload = verifier.verify(body, headers);
    accepted = true;
  } catch (error: unknown) {
    if (!(error instanceof WebhookVerificationError)) {
      throw error;
    }
  }

  const tamperedHeaders = {
    ...headers,
    "webhook-signature": tamperSignature(headers["webhook-signature"] ?? ""),
  };
  let tamperedRejected = false;
  try {
    verifier.verify(body, tamperedHeaders);
  } catch (error: unknown) {
    if (!(error instanceof WebhookVerificationError)) {
      throw error;
    }
    tamperedRejected = true;
  }

  return {
    messageId: headers["webhook-id"] ?? "",
    timestamp: headers["webhook-timestamp"] ?? "",
    payload,
    payloadBase64: body.toString("base64"),
    accepted,
    tamperedRejected,
  };
}

function appendReceipt(path: string, receipt: Receipt): void {
  appendFileSync(path, `${JSON.stringify(receipt)}\n`, {
    encoding: "utf8",
    flag: "a",
  });
}

function respond(response: ServerResponse, status: number): void {
  response.writeHead(status, { "content-length": "0" });
  response.end();
}

async function handleRequest(
  request: IncomingMessage,
  response: ServerResponse,
  verifier: Webhook,
  resultPath: string,
): Promise<void> {
  if (request.method === "GET" && request.url === "/health") {
    respond(response, 204);
    return;
  }
  if (request.url !== "/webhook") {
    respond(response, 404);
    return;
  }
  if (request.method !== "POST") {
    respond(response, 405);
    return;
  }

  const body = await readRawBody(request);
  const headers = verificationHeaders(request);
  const receipt = verifyReceipt(verifier, body, headers);
  appendReceipt(resultPath, receipt);

  if (!receipt.accepted) {
    respond(response, 401);
    return;
  }
  if (!receipt.tamperedRejected) {
    respond(response, 500);
    return;
  }

  respond(response, 204);
}

const address = parseAddress(requireEnvironment("RECEIVER_ADDR"));
const verifier = new Webhook(requireEnvironment("RECEIVER_SECRET"));
const resultPath = requireEnvironment("RECEIVER_RESULT");

const server = createServer((request, response) => {
  void handleRequest(request, response, verifier, resultPath).catch(
    (error: unknown) => {
      console.error(error);
      if (!response.headersSent) {
        respond(response, 500);
        return;
      }
      response.end();
    },
  );
});

server.listen(address.port, address.host);
