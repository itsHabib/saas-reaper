# Slack escalation pilot

Goal: replace the current tunnel with a stable HTTPS callback that reaches the existing local
escalation app, with minimal setup and a measured annual cost below the operator's approximate
$100/year ngrok spend. The current ngrok bill and current callback configuration are unverified.

The loopback demo sends a synthetic Slack v0 signed form body through edge → link → agent →
origin. A valid request must receive 200 within three seconds; a modified body and a correctly
signed but stale timestamp must receive 401. This proves byte/header transport and the synthetic
receiver's rejection behavior. It does not exercise Slack itself or the real escalation app.

Before a live pilot, record the GCP project, delegated DNS zone/domain, local receiver port,
callback path, Slack test-app identifier, and current ngrok URL for rollback. Keep the signing
secret in the local app. Never put it on the public tunnel host.

1. Apply a reviewed plan, inspect startup output, and verify HTTPS health from the allowed
   control IP. Confirm the server survives a VM replacement with the same claim and certificate.
2. Create a dedicated `slack` claim and point its agent at the existing local callback listener.
   Leave the existing ngrok setup available during the pilot.
3. Test a signed request against the actual receiver through the new hostname. Use the app's
   test/dry-run mode so this does not resolve or approve a real escalation. Confirm its signature
   and replay protections, and that acknowledgement remains under three seconds.
4. With operator approval, change a **test Slack app** callback URL and perform one harmless
   escalation interaction. Record its request identifier, receipt in the app, HTTP result,
   latency, and any retry header. Do not infer a successful callback from a sent Slack message.
5. Disconnect and reconnect the agent; confirm offline requests fail, recovery works, and
   duplicate Slack deliveries do not duplicate the app's action. Idempotency belongs to the app.
6. Only after successful live evidence, switch the production callback URL with its owner.
   Roll back by restoring the saved ngrok URL. Cancel ngrok only after an agreed observation
   window and a measured GCP bill projection that includes free-tier usage elsewhere.

No live infrastructure, Slack settings, messages, or ngrok subscription were changed by the
local proof. Required operator inputs remain outstanding.

Protocol: [Slack signature verification](https://docs.slack.dev/authentication/verifying-requests-from-slack/).
