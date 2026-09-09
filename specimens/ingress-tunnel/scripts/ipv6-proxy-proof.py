"""Exercise the real IPv6-mode Caddy policy on loopback, without DNS or ACME."""
import http.client
import http.server
import json
import pathlib
import subprocess
import sys
import threading
import time

binary, config_path, work_dir = sys.argv[1:]
config = json.loads(pathlib.Path(config_path).read_text())
config['admin'] = {'disabled': True}
config['apps'].pop('tls', None)
servers = config['apps']['http']['servers']
# The same routes run without TLS on a test-only loopback listener. The adapter
# input trusts loopback as the simulated Cloudflare peer; production does not.
routes = []
logger_names = {}
for server in servers.values():
    routes.extend(server.get('routes', []))
    logger_names.update(server.get('logs', {}).get('logger_names', {}))
server = next(iter(servers.values()))
server['listen'] = ['127.0.0.1:19507']
server['automatic_https'] = {'disable': True}
server.pop('tls_connection_policies', None)
server['routes'] = routes
server.setdefault('logs', {})['logger_names'] = logger_names
config['apps']['http']['servers'] = {'proof': server}

def replace_upstream(value):
    if isinstance(value, dict):
        if value.get('handler') == 'reverse_proxy':
            value['upstreams'] = [{'dial': '127.0.0.1:19508'}]
        for child in value.values():
            replace_upstream(child)
    if isinstance(value, list):
        for child in value:
            replace_upstream(child)

replace_upstream(config)
rendered = pathlib.Path(work_dir) / 'ipv6-proof.json'
rendered.write_text(json.dumps(config))

class Target(http.server.BaseHTTPRequestHandler):
    calls = 0
    def do_GET(self):
        Target.calls += 1
        body = json.dumps({'headers': dict(self.headers), 'call': Target.calls}).encode()
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'public, max-age=300')
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_args):
        pass

target = http.server.ThreadingHTTPServer(('127.0.0.1', 19508), Target)
threading.Thread(target=target.serve_forever, daemon=True).start()
log_path = pathlib.Path(work_dir) / 'ipv6-proof.log'
with log_path.open('w') as logs:
    process = subprocess.Popen([binary, 'run', '--config', str(rendered)], stdout=logs, stderr=logs)
    try:
        def request(host='control.example.com', key='fixture-origin-secret', visitor='203.0.113.7'):
            conn = http.client.HTTPConnection('127.0.0.1', 19507, timeout=3)
            try:
                conn.request('GET', '/dynamic.js', headers={
                    'Host': host, 'X-Reaper-Origin': key,
                    'CF-Connecting-IP': visitor, 'X-Forwarded-For': '192.0.2.99'})
                response = conn.getresponse()
                return response.status, dict(response.getheaders()), response.read()
            finally:
                conn.close()
        for attempt in range(50):
            if process.poll() is not None:
                raise RuntimeError(log_path.read_text())
            try:
                first = request()
                break
            except ConnectionRefusedError:
                time.sleep(.1)
        assert first[0] == 200, first
        payload = json.loads(first[2])
        assert payload['headers']['X-Forwarded-For'] == '203.0.113.7', payload
        assert 'X-Reaper-Origin' not in payload['headers'], payload
        assert 'Cf-Connecting-Ip' not in payload['headers'], payload
        assert first[1].get('Cache-Control') == 'no-store', first
        assert request(host='control.example.com:8443')[0] == 200
        assert request(host='control.example.com:8443', key='wrong-origin-secret')[0] == 403
        before_unknown = Target.calls
        request(host='unmatched.example.com')
        request(host='unmatched.example.com:8443')
        assert Target.calls == before_unknown
        second = request()
        assert json.loads(second[2])['call'] != payload['call']
        for args in [dict(key='wrong-origin-secret'), dict(key=''),
                     dict(visitor='198.51.100.2'), dict(visitor='203.0.113.7, 198.51.100.2'),
                     dict(visitor='garbage'), dict(visitor=''), dict(visitor='2001:db8::7')]:
            assert request(**args)[0] == 403, args
        assert request(host='probe.example.com')[0] == 200
        assert request(host='probe.example.com', visitor='2001:db8::7')[0] == 200
        assert request(host='probe.example.com', key='wrong-origin-secret')[0] == 403
        # A refused upstream produces http.log.error, not just an access record.
        # Keep the production default logger: filtering only access logs leaks here.
        target.shutdown()
        target.server_close()
        assert request()[0] == 502
        assert request(host='probe.example.com')[0] == 502
        assert request(key='wrong-origin-secret')[0] == 403
        assert request(key='')[0] == 403
    finally:
        process.terminate()
        process.wait(timeout=10)
        target.shutdown()
        target.server_close()
assert 'fixture-origin-secret' not in log_path.read_text()
assert 'wrong-origin-secret' not in log_path.read_text()
print('IPv6 Caddy policy: visitor CIDRs, origin secret, forwarding, no-store and log redaction pass')
