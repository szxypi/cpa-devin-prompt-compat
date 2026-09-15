"""Compiled C ABI plus isolated CPA, including hot activation and rollback."""
import base64
import ctypes
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time

import requests
import yaml
from abi import HostAPI, PluginAPI, invoke

ROOT = Path(__file__).resolve().parents[1]
NAME = 'cpa-devin-prompt-compat'
LIB = ROOT / 'dist' / (NAME + '-v0.1.1.so')
IDENTITY = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
IDENTITY_REPLACED = "You are an agent built with the Claude Agent SDK."
EMOJI_SENTENCE = "For clear communication with the user the assistant MUST avoid using emojis."
EMOJI_REPLACED = "Avoid emojis so that communication with the user stays clear."


def payload(model='devin/claude-code', stream=False):
    return {'model': model, 'stream': stream, 'max_tokens': 1024,
            'system': [{'type': 'text', 'text': IDENTITY + '\n' + EMOJI_SENTENCE}],
            'messages': [{'role': 'user', 'content': 'hello'}]}


def abi_check():
    lib = ctypes.CDLL(str(LIB))
    lib.cliproxy_plugin_init.argtypes = [ctypes.POINTER(HostAPI), ctypes.POINTER(PluginAPI)]
    api = PluginAPI()
    assert lib.cliproxy_plugin_init(ctypes.byref(HostAPI(abi_version=1)), ctypes.byref(api)) == 0
    cfg = lambda text: {'config_yaml': base64.b64encode(text.encode()).decode()}
    invoke(api, 'plugin.register', cfg('log-stats: false\n'))
    body = payload()
    request = {'SourceFormat': 'claude', 'RequestedModel': body['model'], 'Body': base64.b64encode(json.dumps(body).encode()).decode()}
    changed = invoke(api, 'request.intercept_before', request)
    result = json.loads(base64.b64decode(changed['Body']))
    expected = dict(body, system=[{'type': 'text', 'text': IDENTITY_REPLACED + '\n' + EMOJI_REPLACED}])
    assert result == expected, result
    invoke(api, 'plugin.reconfigure', cfg('enabled: false\n'))
    assert not invoke(api, 'request.intercept_before', request).get('Body')
    invoke(api, 'plugin.reconfigure', cfg('enabled: true\n'))
    other_model = dict(request, RequestedModel='gpt-5.6')
    assert not invoke(api, 'request.intercept_before', other_model).get('Body')
    api.shutdown()


def integration():
    captured = []

    class Mock(BaseHTTPRequestHandler):
        def log_message(self, *args): pass
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            captured.append(body)
            text = ' '.join(b.get('text', '') for b in body.get('system', []))
            blocked = IDENTITY in text or EMOJI_SENTENCE in text
            if blocked:
                data = json.dumps({'type': 'error', 'error': {'type': 'permission_error', 'message': 'fixture content policy'}}).encode()
                status, content_type = 403, 'application/json'
            else:
                message = {'id': 'msg_fixture', 'type': 'message', 'role': 'assistant', 'model': body['model'],
                           'content': [{'type': 'text', 'text': 'ok'}], 'stop_reason': 'end_turn', 'stop_sequence': None,
                           'usage': {'input_tokens': 10, 'output_tokens': 1}}
                status, content_type = 200, 'application/json'
                data = json.dumps(message).encode()
                if body.get('stream'):
                    events = [{'type': 'message_start', 'message': dict(message, content=[], stop_reason=None)},
                              {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
                              {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': 'ok'}},
                              {'type': 'content_block_stop', 'index': 0},
                              {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn'}, 'usage': {'output_tokens': 1}},
                              {'type': 'message_stop'}]
                    data = ''.join('event: ' + e['type'] + '\ndata: ' + json.dumps(e) + '\n\n' for e in events).encode()
                    content_type = 'text/event-stream'
            self.send_response(status)
            self.send_header('Content-Type', content_type)
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    mock = ThreadingHTTPServer(('127.0.0.1', 0), Mock)
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix=NAME+'-') as directory:
            work = Path(directory)
            plugins = work / 'plugins/linux/amd64'
            plugins.mkdir(parents=True)
            shutil.copy2(LIB, plugins / LIB.name)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            config = {'host': '127.0.0.1', 'port': port, 'auth-dir': str(work/'auth'), 'api-keys': ['fixture-only'],
                      'request-retry': 0, 'remote-management': {'disable-control-panel': True},
                      'plugins': {'enabled': True, 'dir': str(work/'plugins'), 'configs': {NAME: {'enabled': False}}},
                      'claude-api-key': [{'api-key': 'mock-only', 'base-url': f'http://127.0.0.1:{mock.server_port}',
                                          'cloak': {'mode': 'never'}, 'disable-cooling': True,
                                          'models': [{'name': 'devin/claude-code', 'alias': 'devin/claude-code'}, {'name': 'gpt-5.6', 'alias': 'gpt-5.6'}]}]}
            config_file = work/'config.yaml'

            def save():
                with config_file.open('w') as f:
                    f.write(yaml.safe_dump(config)); f.flush(); os.fsync(f.fileno())

            save()
            with (work/'server.log').open('w') as log:
                proc = subprocess.Popen(['/usr/local/bin/cli-proxy-api', '-config', str(config_file)], cwd=work,
                                        stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
            session = requests.Session(); session.trust_env = False
            session.headers.update({'x-api-key': 'fixture-only', 'anthropic-version': '2023-06-01'})
            base = f'http://127.0.0.1:{port}'
            try:
                end = time.monotonic()+15
                while time.monotonic() < end:
                    if proc.poll() is not None: raise AssertionError('isolated CPA exited')
                    try:
                        r = session.get(base+'/v1/models', timeout=1)
                        if any(m.get('display_name', m['id'])=='devin/claude-code' for m in r.json().get('data', [])) and 'file watcher started' in (work/'server.log').read_text(): break
                    except requests.RequestException: pass
                    time.sleep(0.1)
                else: raise AssertionError('startup timeout: '+r.text[:1500])
                send = lambda body: session.post(base+'/v1/messages', json=body, timeout=15)
                # Plugin disabled: upstream sees the original sentences and rejects with 403.
                assert send(payload()).status_code == 403
                config['plugins']['configs'][NAME]['enabled'] = True
                save()
                end = time.monotonic()+8
                while time.monotonic() < end:
                    if send(payload()).status_code == 200: break
                    time.sleep(0.2)
                else: raise AssertionError('hot enable failed')
                for stream in [False, True]:
                    body = payload(stream=stream)
                    r = send(body)
                    assert r.status_code == 200, r.status_code
                    sent = captured[-1]
                    assert sent['system'][0]['text'] == IDENTITY_REPLACED + '\n' + EMOJI_REPLACED, sent['system']
                    if stream:
                        assert 'message_stop' in r.text
                    else:
                        assert r.json()['content'][0]['text'] == 'ok'
                # Non-devin model is left untouched and still rejected upstream.
                assert send(payload('gpt-5.6')).status_code == 403
                assert captured[-1]['system'][0]['text'] == payload('gpt-5.6')['system'][0]['text']
                config['plugins']['configs'][NAME]['enabled'] = False
                save()
                end = time.monotonic()+8
                while time.monotonic() < end:
                    if send(payload()).status_code == 403: break
                    time.sleep(0.2)
                else: raise AssertionError('hot disable failed')
                assert proc.poll() is None
            finally:
                proc.terminate()
                try: proc.wait(timeout=10)
                except subprocess.TimeoutExpired: proc.kill(); proc.wait()
                (ROOT/'dist/integration-server.log').write_text((work/'server.log').read_text())
    finally:
        mock.shutdown(); mock.server_close()


abi_check()
integration()
result = {'abi': 'passed', 'streaming_and_nonstreaming': 'passed', 'other_model_unchanged': 'passed',
          'hot_enable_and_disable_same_pid': 'passed'}
(ROOT/'dist/integration-result.json').write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps(result))
