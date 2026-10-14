#!/usr/bin/env python3
"""Isolated, synthetic Meilisearch/Community API acceptance and bounded load.

Uses the Python standard library. Never point this at an existing application DB.
Example (paths are deployment-specific, no credential values go in arguments):
  python3 scripts/recommendation/meili_api_acceptance.py prepare \
    --binary /tmp/community-discovery-qa/community \
    --mysql-defaults /tmp/community-feed-perf/mysql/client.cnf \
    --mysql-client /tmp/community-feed-perf/mysql/mysql-client.sh
  python3 scripts/recommendation/meili_api_acceptance.py start
  python3 scripts/recommendation/meili_api_acceptance.py seed
  python3 scripts/recommendation/meili_api_acceptance.py lexical

Runtime state, credentials and evidence stay in --runtime, never in the checkout.
Only this harness's PID files are stopped. The model proxy permits loopback only
and records counts, not corpus text. No production-quality assertion is implied.
"""
from __future__ import annotations
import argparse
import concurrent.futures
import configparser
import hashlib
import http.server as http_server
import ipaddress
import math
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_RUNTIME = Path('/tmp/community-meili-api-acceptance')
DEFAULTS = {'database': 'community_discovery_qa_meili_acceptance', 'port': 18120,
            'peer_port': 18121, 'redis_port': 17779, 'redis_db': 4,
            'meili_url': 'http://127.0.0.1:17700', 'meili_route': 'a',
            'prefix': 'meili_api_acceptance_20261014_', 'proxy_port': 18124, 'embedding_proxy_port': 18126,
            'binary': '/tmp/community-discovery-qa/community',
            'mysql_defaults': '/tmp/community-feed-perf/mysql/client.cnf',
            'mysql_client': '/tmp/community-feed-perf/mysql/mysql-client.sh'}


def dump(path: Path, data, private=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    mode=0o600 if private else 0o644
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,mode)
    with os.fdopen(fd,'w') as stream:
        json.dump(data,stream,ensure_ascii=False,indent=2)
    path.chmod(mode)


def loopback_url(value):
    u = urllib.parse.urlsplit(value)
    if u.scheme != 'http' or u.username or u.password or not u.hostname:
        raise ValueError('QA service must be an explicit HTTP loopback URL')
    if not ipaddress.ip_address(u.hostname).is_loopback:
        raise ValueError('QA service must stay on loopback; external corpus transfer prohibited')
    return value.rstrip('/')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


OPENER = urllib.request.build_opener(NoRedirect)


def http(method, url, body=None, token=None, timeout=15, form=False):
    headers = {}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    data = None
    if body is not None:
        data = urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode()
        headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        response = OPENER.open(request, timeout=timeout)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read(16 * 1024 * 1024 + 1)
        if len(raw) > 16 * 1024 * 1024:
            raise ValueError('QA response budget exceeded')
        try:
            payload = json.loads(raw)
        except ValueError:
            payload = {'non_json': True}
        return response.status, dict(response.headers), payload


class Acceptance:
    def __init__(self, args):
        self.root = args.runtime.resolve()
        self.root.mkdir(parents=True, exist_ok=True)
        self.settings_path = self.root / 'settings.json'
        self.settings = dict(DEFAULTS)
        saved=json.loads(self.settings_path.read_text()) if self.settings_path.exists() else {}
        self.settings.update(saved)
        for key in DEFAULTS:
            value = getattr(args, key, None)
            if value is not None:
                self.settings[key] = str(value) if isinstance(value, Path) else value
        for key in ['database','redis_port','redis_db','prefix']:
            if key in saved and self.settings[key]!=saved[key]:
                raise ValueError('QA resource identity is immutable; use a new runtime and unused resources')
        self.settings['meili_url'] = loopback_url(self.settings['meili_url'])
        if not re.fullmatch(r'community_discovery_qa_[a-z0-9_]+', self.settings['database']):
            raise ValueError('Refusing non-QA database')
        if not re.fullmatch(r'meili_api_acceptance_[a-z0-9_]+', self.settings['prefix']):
            raise ValueError('Refusing non-QA index prefix')
        self.fixture_path = self.root / 'fixture.private.json'
        self.fixture = json.loads(self.fixture_path.read_text()) if self.fixture_path.exists() else {'accounts': [], 'statuses': []}
        self.base = 'http://127.0.0.1:' + str(self.settings['port'])
        self.args = args

    def record(self, name, value):
        dump(self.root / (name + '.json'), value)
        print(json.dumps({'evidence': name, **value}, ensure_ascii=False), flush=True)

    def sql(self, statement):
        # The credential binding stays in its existing file and never appears in argv/output.
        result = subprocess.run([self.settings['mysql_client'], '--defaults-extra-file=' + self.settings['mysql_defaults'], '-N'], input=statement, text=True, capture_output=True)
        if result.returncode:
            raise RuntimeError('Isolated SQL operation failed: ' + result.stderr[:600])
        return result.stdout

    def ownership(self):
        secret=self.root/'encryption.private'
        if not secret.exists():
            fd=os.open(secret,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
            with os.fdopen(fd,'w') as stream:stream.write(secrets.token_hex(32))
        return hashlib.sha256((secret.read_text()+str(self.root)+self.settings['database']+str(self.settings['redis_db'])).encode()).hexdigest()

    def verify_ownership(self):
        expected=self.ownership()
        value=self.sql('SELECT owner FROM `'+self.settings['database']+'`.qa_acceptance_ownership LIMIT 1;').strip()
        redis_owner=self.redis_commands([['GET','community:qa:meili-api-acceptance:owner']])[0]
        if value!=expected or redis_owner!=expected:
            raise RuntimeError('Refusing QA resources owned by a different runtime')

    def prepare(self):
        expected=self.ownership()
        if self.settings_path.exists():
            self.verify_ownership()
        else:
            exists=self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='"+self.settings['database']+"';").strip()
            size=self.redis_commands([['DBSIZE']])[0]
            if exists!='0' or size!=0:
                raise RuntimeError('First prepare requires an unused database and empty dedicated Redis DB')
            self.sql('CREATE DATABASE `'+self.settings['database']+'` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE TABLE `'+self.settings['database']+'`.qa_acceptance_ownership (owner VARCHAR(64) PRIMARY KEY); INSERT INTO `'+self.settings['database']+"`.qa_acceptance_ownership VALUES ('"+expected+"');")
            owned=self.redis_commands([['SET','community:qa:meili-api-acceptance:owner',expected,'NX']])[0]
            if owned!='OK':raise RuntimeError('Dedicated Redis ownership changed during prepare')
        dump(self.settings_path,self.settings)
        self.record('environment',{'database':self.settings['database'],'ports':[self.settings['port'],self.settings['peer_port']],'redis_db':self.settings['redis_db'],'prefix':self.settings['prefix'],'meili_url':self.settings['meili_url']})

    def api(self, path, who=0, method='GET', body=None, expected=200, peer=False):
        token = self.fixture['accounts'][who]['token'] if who is not None else None
        base = 'http://127.0.0.1:' + str(self.settings['peer_port']) if peer else self.base
        status, headers, payload = http(method, base + path, body, token)
        choices = [expected] if isinstance(expected, int) else expected
        if status not in choices:
            raise AssertionError(f'{method} {path}: HTTP {status}; expected {choices}; code={payload.get("code") if isinstance(payload,dict) else None}')
        return payload

    def environment(self, peer=False):
        c = configparser.ConfigParser(); c.read(self.settings['mysql_defaults']); creds = c['client']
        env = os.environ.copy()
        # Ignore inherited embedder settings until this isolated instance opts in.
        for key in list(env):
            if key.startswith('RECOMMENDATION_EMBEDDER_'):
                del env[key]
        runtime = self.root / ('peer' if peer else 'primary'); runtime.mkdir(exist_ok=True)
        env.update(LISTEN='127.0.0.1:' + str(self.settings['peer_port'] if peer else self.settings['port']), APP_BASE_URL=self.base,
                   APP_ROOT=str(runtime), DEV_MODE='true', STORAGE_TYPE='local', ENCRYPTED_KEY=(self.root/'encryption.private').read_text(),
                   DB_CONNECTION='mysql', DB_HOST='127.0.0.1', DB_PORT='13316', DB_USERNAME=creds['user'], DB_PASSWORD=creds.get('password',''), DB_DATABASE=self.settings['database'], DB_DSN='',
                   DB_MAX_OPEN_CONNS='4', DB_MAX_IDLE_CONNS='2', GOMAXPROCS='1', REDIS_0_HOST='127.0.0.1', REDIS_0_PORT=str(self.settings['redis_port']), REDIS_0_DB=str(self.settings['redis_db']),
                   MEILISEARCH_ENABLED='true', MEILISEARCH_HOST=('http://127.0.0.1:'+str(self.settings['proxy_port'])+'/'+self.settings['meili_route']) if (self.root/'proxy.pid').exists() else self.settings['meili_url'], MEILISEARCH_INDEX_PREFIX=self.settings['prefix'], FEED_MODE='legacy', COMMUNITY_ROLE='all')
        model_path = self.root / 'model.private.json'
        if model_path.exists():
            model = json.loads(model_path.read_text())
            env.update(RECOMMENDATION_EMBEDDER_NAME=model['name'], RECOMMENDATION_EMBEDDER_MODEL_VERSION=model['version'], RECOMMENDATION_EMBEDDER_CONFIG=json.dumps(model['config']),
                       RECOMMENDATION_EMBEDDER_MAX_DOCUMENTS=str(model.get('max_documents',500)), RECOMMENDATION_EMBEDDER_MAX_UNITS_PER_HOUR=str(model.get('max_units_per_hour',10000)), RECOMMENDATION_EMBEDDER_BUDGET_APPROVED='true')
        return env

    def start(self, peer=False):
        name = 'peer' if peer else 'primary'; pidfile = self.root/(name+'.pid')
        if pidfile.exists():
            try:
                os.kill(int(pidfile.read_text()), 0)
                raise RuntimeError('QA ' + name + ' is already running')
            except ProcessLookupError:
                pass
        env = self.environment(peer)
        with (self.root/(name+'.log')).open('ab') as log:
            process = subprocess.Popen([self.settings['binary']], cwd=env['APP_ROOT'], env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        pidfile.write_text(str(process.pid))
        (self.root/(name+'.identity')).write_text(Path('/proc/'+str(process.pid)+'/stat').read_text().split()[21])
        url = ('http://127.0.0.1:' + str(self.settings['peer_port']) if peer else self.base) + '/api/v1/instance'
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError('QA process exited; inspect isolated log')
            try:
                if http('GET', url, timeout=1)[0] == 200:
                    print('Ready ' + name, flush=True); return
            except (OSError, ValueError):
                pass
            time.sleep(.2)
        raise RuntimeError('QA service readiness timed out')

    def stop(self, peer=False, proxy=False, embedding=False):
        name = 'embedding-proxy' if embedding else ('proxy' if proxy else ('peer' if peer else 'primary')); path = self.root/(name+'.pid')
        if not path.exists(): return
        pid = int(path.read_text())
        try:
            # Verify the recorded process start time before terminating this PID.
            identity = self.root/(name+'.identity')
            actual = Path('/proc/'+str(pid)+'/stat').read_text().split()[21]
            if not identity.exists() or identity.read_text() != actual:
                raise RuntimeError('Refusing to stop a reused process ID')
            os.kill(pid, signal.SIGTERM)
            for _ in range(100):
                try:
                    state = Path('/proc/'+str(pid)+'/stat').read_text().split()[2]
                    if state == 'Z': break
                except FileNotFoundError: break
                time.sleep(.05)
            else: raise RuntimeError('QA process did not stop gracefully')
        except (ProcessLookupError,FileNotFoundError): pass
        path.unlink(missing_ok=True)
        print('Stopped own ' + name, flush=True)

    def seed(self):
        def save(): dump(self.fixture_path, self.fixture, True)
        for number in range(len(self.fixture['accounts']), 24):
            name = 'meiliqa' + str(number); password = secrets.token_hex(24)
            body = {'user[account_attributes][username]':name, 'user[account_attributes][display_name]':'Synthetic author '+str(number), 'user[email]':name+'@example.test', 'user[password]':password, 'user[password_confirmation]':password}
            status, _, _ = http('POST',self.base+'/api/v1/accounts/register',body,form=True)
            if status != 200: raise RuntimeError('Synthetic registration failed '+str(status))
            status, _, login = http('POST',self.base+'/api/v1/accounts/login',{'username':name,'password':password})
            if status != 200: raise RuntimeError('Synthetic login failed '+str(status))
            account={'username':name,'password':password,'token':login.get('token') or login['access_token']}
            account['id']=http('GET',self.base+'/api/v1/accounts/verify_credentials',token=account['token'])[2]['id']
            self.fixture['accounts'].append(account);save()
            self.api('/api/v1/accounts/update_credentials',number,'PATCH',{'discoverable':number!=23,'indexable':True})
        topics=[('摄影','日落山川与街头相机。Photography captures a sunset over the mountains.'),('programming','Go concurrency and SQL query optimization. 并发程序与数据库。'),('music','ピアノの演奏とジャズ。Piano music and jazz improvisation.'),('猫','猫と一緒に過ごす週末。A cat resting peacefully on a sofa.')]
        for n,account in enumerate(self.fixture['accounts']):
            for topic,text in topics:
                if any(x['author']==account['id'] and x.get('topic')==topic for x in self.fixture['statuses']):continue
                payload=self.api('/api/v1/statuses',n,'POST',{'status':text+' #'+topic+' #SyntheticAcceptance','visibility':'public','language':'zh' if topic in ['摄影','猫'] else 'en'})
                self.fixture['statuses'].append({'id':payload['id'],'author':account['id'],'topic':topic,'visibility':'public','eligible':n!=23});save()
        for visibility in ['unlisted','private','direct']:
            if any(x['visibility']==visibility for x in self.fixture['statuses']):continue
            payload=self.api('/api/v1/statuses',2,'POST',{'status':('@meiliqa1 ' if visibility=='direct' else '')+'Synthetic private control #NeverRecommendMe','visibility':visibility})
            self.fixture['statuses'].append({'id':payload['id'],'author':self.fixture['accounts'][2]['id'],'visibility':visibility,'eligible':False});save()
        self.sql('USE `'+self.settings['database']+"`; UPDATE users SET is_admin=1 WHERE username='meiliqa0'; INSERT INTO system_settings (`key`,val) VALUES ('config.recommendation.enabled','true') ON DUPLICATE KEY UPDATE val='true';")
        self.api('/api/v1/discovery/preferences',0,'PUT',{'preference':'personalized','languages':[]})
        for topic in ['摄影','programming','music','猫']:
            self.api('/api/v1/tags/'+urllib.parse.quote(topic)+'/follow',0,'POST')
        self.record('seed',{'authors':len(self.fixture['accounts']),'statuses':len(self.fixture['statuses']),'eligible':sum(x['eligible'] for x in self.fixture['statuses'])})

    def wait(self, check, timeout=160, label='background state'):
        deadline=time.monotonic()+timeout; last=None
        while time.monotonic()<deadline:
            try:last=self.api('/api/v1/admin/discovery/status')
            except (OSError,urllib.error.URLError):
                time.sleep(2);continue
            if check(last): return last
            time.sleep(2)
        self.record('timeout',{'label':label,'last':last})
        raise AssertionError(label+' did not become ready')

    def set_approval(self, value):
        self.api('/api/v1/admin/settings/performance',0,'PUT',{'values':{'config.recommendation.meili_approved_version':value}})

    def approve(self):
        status=self.wait(lambda x:bool(x['index'].get('version')),label='index version')
        self.set_approval(status['index']['version'])
        return status['index']['version']

    def check_mode(self, expected):
        self.wait(lambda x:x['index']['ready'] and x['runtime'].get('pool_mode')==expected,timeout=180,label=expected+' pool')
        # Diagnostics describe the raw pool; a policy change can already make
        # reads fall back before that pool's next scheduled rebuild.
        deadline=time.monotonic()+180
        while True:
            first=self.api('/api/v1/discovery/statuses?limit=20')
            if first['mode']==expected:break
            if time.monotonic()>=deadline:raise AssertionError((first['mode'],expected))
            time.sleep(2)
        assert len(first['items'])==20
        assert first['next_cursor']
        second=self.api('/api/v1/discovery/statuses?limit=20&cursor='+urllib.parse.quote(first['next_cursor']))
        ids={x['status']['id'] for x in first['items']}
        assert not ids.intersection(x['status']['id'] for x in second['items'])
        eligible={x['id'] for x in self.fixture['statuses'] if x['eligible']}
        assert all(x['status']['id'] in eligible for x in first['items']+second['items'])
        self.record(expected,{'mode':expected,'items':len(first['items']),'second_page_items':len(second['items']),'approved_version':self.api('/api/v1/admin/discovery/status')['approved_version']})
        return first

    def revocation(self):
        before=self.api('/api/v1/admin/discovery/status')['approved_version']
        first=self.api('/api/v1/discovery/statuses?limit=5')
        assert first['next_cursor']
        try:
            self.set_approval('')
            self.api('/api/v1/discovery/statuses?cursor='+urllib.parse.quote(first['next_cursor']),expected=409)
            after=self.api('/api/v1/discovery/statuses?limit=20')
            assert after['mode']=='rules',after['mode']
            self.record('approval-revocation',{'old_cursor_status':409,'new_mode':after['mode']})
        finally:self.set_approval(before)

    def privacy(self):
        page=self.api('/api/v1/discovery/statuses?limit=1')
        assert page['next_cursor']
        following=self.api('/api/v1/discovery/statuses?limit=20&cursor='+urllib.parse.quote(page['next_cursor']))
        selected=following['items'][0]['status'];target=selected['id'];author=selected['account']['id']
        index_before=self.api('/api/v1/admin/discovery/status')['index']['generation']
        index=self.settings['prefix']+'discovery_statuses_v1_'+index_before
        document_url=self.settings['meili_url']+'/indexes/'+urllib.parse.quote(index)+'/documents/'+urllib.parse.quote(target)
        assert http('GET',document_url)[0]==200,'Target must exist in stale index before withdrawal'
        # Only synthetic rows belonging to this fixture can be mutated.
        assert any(x['id']==target for x in self.fixture['statuses'])
        assert any(x['id']==author for x in self.fixture['accounts'])
        account_no=next(i for i,x in enumerate(self.fixture['accounts']) if x['id']==author)
        db=self.settings['database']
        try:
            self.sql(f'USE `{db}`; UPDATE statuses SET visibility=2 WHERE id={int(target)};')
            stale=self.api('/api/v1/discovery/statuses?limit=20&cursor='+urllib.parse.quote(page['next_cursor']))
            assert target not in {x['status']['id'] for x in stale['items']}
            self.sql(f'USE `{db}`; UPDATE statuses SET visibility=0 WHERE id={int(target)};')
            self.api('/api/v1/accounts/update_credentials',account_no,'PATCH',{'discoverable':False})
            stale=self.api('/api/v1/discovery/statuses?limit=20&cursor='+urllib.parse.quote(page['next_cursor']))
            assert author not in {x['status']['account']['id'] for x in stale['items']}
            index_after=self.api('/api/v1/admin/discovery/status')['index']['generation']
            assert index_before==index_after,'Index refreshed during immediate-revocation experiment; rerun for stale-index evidence'
            assert http('GET',document_url)[0]==200,'Target must still exist in stale index after SQL/consent withdrawal'
            self.record('stale-index-privacy',{'index_generation_unchanged':True,'private_status_absent':True,'withdrawn_author_absent':True})
        finally:
            self.sql(f'USE `{db}`; UPDATE statuses SET visibility=0 WHERE id={int(target)};')
            self.api('/api/v1/accounts/update_credentials',account_no,'PATCH',{'discoverable':True})

    def redis_commands(self,commands):
        def read(stream):
            line=stream.readline();kind=line[:1];value=line[1:-2]
            if kind==b'$':
                n=int(value)
                if n<0:return None
                result=stream.read(n);stream.read(2);return result.decode()
            if kind==b':':return int(value)
            if kind==b'+':return value.decode()
            raise RuntimeError('QA Redis protocol error')
        def wire(parts):return ('*'+str(len(parts))+'\r\n').encode()+b''.join(('$'+str(len(x.encode()))+'\r\n'+x+'\r\n').encode() for x in parts)
        with socket.create_connection(('127.0.0.1',self.settings['redis_port']),timeout=1) as conn:
            conn.sendall(wire(['SELECT',str(self.settings['redis_db'])])+b''.join(wire(parts) for parts in commands))
            stream=conn.makefile('rb');read(stream)
            return [read(stream) for _ in commands]

    def redis_lease(self):
        key='community:discovery:{meili-'+self.settings['prefix']+'}:lease'
        token,ttl=self.redis_commands([['GET',key],['PTTL',key]])
        return {'owner':hashlib.sha256(token.encode()).hexdigest()[:16] if token else None,'pttl_ms':ttl}

    def proxy_start(self,embedding=False):
        name='embedding-proxy' if embedding else 'proxy'
        path=self.root/(name+'.pid')
        if path.exists():
            try:
                pid=int(path.read_text());os.kill(pid,0)
                if Path('/proc/'+str(pid)+'/stat').read_text().split()[2]!='Z':
                    print('Proxy already running');return
            except ProcessLookupError: pass
        with (self.root/(name+'.log')).open('ab') as log:
            proc=subprocess.Popen([os.sys.executable,str(Path(__file__).resolve()),name,'--runtime',str(self.root)],stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
        path.write_text(str(proc.pid))
        (self.root/(name+'.identity')).write_text(Path('/proc/'+str(proc.pid)+'/stat').read_text().split()[21])
        for _ in range(50):
            try:
                if http('GET','http://127.0.0.1:'+str(self.settings['embedding_proxy_port' if embedding else 'proxy_port'])+'/__qa_stats',timeout=1)[0]==200:
                    print('Ready isolated '+name);return
            except OSError:pass
            time.sleep(.1)
        raise RuntimeError('QA proxy startup failed')

    def proxy_control(self,mode='normal',delay=0):
        status,_,payload=http('POST','http://127.0.0.1:'+str(self.settings['proxy_port'])+'/__qa_control',{'mode':mode,'delay':delay})
        assert status==200
        return payload

    def proxy_stats(self):
        return http('GET','http://127.0.0.1:'+str(self.settings['proxy_port'])+'/__qa_stats')[2]

    def embedding_proxy(self):
        # A dedicated forwarding endpoint isolates fault injection from the
        # shared real model. Vectors are never fabricated and text is not logged.
        upstream=json.loads((self.root/'model-upstream.private.json').read_text())
        target=loopback_url(upstream['config']['url'])
        lock=threading.Lock();state={'mode':'normal','requests':0,'failures':0,'upstream_errors':0,'active':0,'maximum_active':0}
        class Handler(http_server.BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def send_json(self,status,value):
                raw=json.dumps(value).encode();self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
            def do_GET(self):
                if self.path!='/__qa_stats':self.send_json(404,{});return
                with lock:copy=dict(state)
                self.send_json(200,copy)
            def do_POST(self):
                n=int(self.headers.get('Content-Length','0'))
                if not 0<=n<=8*1024*1024:self.send_json(413,{});return
                body=self.rfile.read(n)
                if self.path=='/__qa_control':
                    mode=json.loads(body).get('mode')
                    if mode not in ['normal','fail_all']:self.send_json(400,{});return
                    with lock:state['mode']=mode
                    self.send_json(200,{'mode':mode});return
                if self.path!='/embed':self.send_json(404,{});return
                with lock:
                    state['requests']+=1;state['active']+=1;state['maximum_active']=max(state['active'],state['maximum_active']);mode=state['mode']
                try:
                    if mode=='fail_all':
                        with lock:state['failures']+=1
                        self.send_json(503,{'error':'Isolated real-model transport failure'});return
                    headers={k:v for k,v in self.headers.items() if k.lower() in ['content-type','authorization']}
                    req=urllib.request.Request(target,data=body,headers=headers,method='POST')
                    try:response=OPENER.open(req,timeout=15)
                    except urllib.error.HTTPError as error:response=error
                    with response:
                        data=response.read(16*1024*1024+1)
                        if len(data)>16*1024*1024:raise RuntimeError('Embedding response budget exceeded')
                        if response.status!=200:
                            with lock:state['upstream_errors']+=1
                        self.send_response(response.status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                except (OSError,RuntimeError):
                    with lock:state['upstream_errors']+=1
                    try:self.send_json(502,{'error':'Real local model unavailable'})
                    except OSError:pass
                finally:
                    with lock:state['active']-=1
        server=http_server.ThreadingHTTPServer(('127.0.0.1',self.settings['embedding_proxy_port']),Handler);server.daemon_threads=True;server.serve_forever()

    def configure_model(self,path):
        model=json.loads(path.read_text());loopback_url(model['config']['url'])
        assert model.get('name') and model.get('version') and model['config'].get('source')=='rest'
        dump(self.root/'model-upstream.private.json',model,True)
        model['config']['url']='http://127.0.0.1:'+str(self.settings['embedding_proxy_port'])+'/embed'
        dump(self.root/'model.private.json',model,True)

    def resources(self):
        result={};ticks=os.sysconf('SC_CLK_TCK')
        paths={'primary':self.root/'primary.pid','peer':self.root/'peer.pid','meili':Path('/tmp/community-discovery-qa/meili.pid')}
        if (self.root/'model-resource-pidfile').exists():paths['model']=Path((self.root/'model-resource-pidfile').read_text().strip())
        for name,path in paths.items():
            try:
                pid=int(path.read_text());stat=Path('/proc/'+str(pid)+'/stat').read_text().split();memory={}
                for line in Path('/proc/'+str(pid)+'/status').read_text().splitlines():
                    if line.startswith(('VmRSS:','VmHWM:')):memory[line.split(':')[0]]=int(line.split()[1])
                result[name]={'pid':pid,'rss_kib':memory.get('VmRSS',0),'lifetime_hwm_kib':memory.get('VmHWM',0),'cpu_seconds':(int(stat[13])+int(stat[14]))/ticks}
            except (OSError,ValueError):result[name]={'unavailable':True}
        try:
            info=self.redis_commands([['INFO','memory']])[0]
            result['redis_used_memory_bytes']=int(next(x.split(':')[1] for x in info.splitlines() if x.startswith('used_memory:')))
        except (OSError,RuntimeError,ValueError):pass
        return {'at':time.time(),'processes':result}

    def proxy_serve(self):
        qa=self;lock=threading.Lock();state={'mode':'normal','delay':0,'requests':0,'failures':0,'active_searches':0,'maximum_searches':0,'by_route':{},'events':[]}
        class Handler(http_server.BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def send_json(self,status,value):
                raw=json.dumps(value).encode();self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
            def do_GET(self):self.forward()
            def do_POST(self):self.forward()
            def do_PUT(self):self.forward()
            def do_PATCH(self):self.forward()
            def do_DELETE(self):self.forward()
            def forward(self):
                n=int(self.headers.get('Content-Length','0'))
                if n<0 or n>8*1024*1024:self.send_json(413,{'error':'QA body budget exceeded'});return
                body=self.rfile.read(n) if n else None
                if self.path=='/__qa_stats':
                    with lock:copy=json.loads(json.dumps(state))
                    self.send_json(200,copy);return
                if self.path=='/__qa_control' and self.command=='POST':
                    control=json.loads(body or b'{}');mode=control.get('mode','normal');delay=float(control.get('delay',0))
                    if mode not in ['normal','fail_search','fail_all'] or not 0<=delay<=5:self.send_json(400,{'error':'invalid control'});return
                    with lock:state.update(mode=mode,delay=delay)
                    self.send_json(200,{'mode':mode,'delay':delay});return
                parts=self.path.split('/',2)
                if len(parts)<3 or parts[1] not in ['a','b']:self.send_json(404,{'error':'unknown replica'});return
                replica=parts[1];path='/'+parts[2];search=path.split('?')[0].endswith('/search')
                lease={}
                if 'discovery_statuses_v1_' in path and (search or self.command in ['POST','PATCH','DELETE']):
                    try:lease=qa.redis_lease()
                    except (OSError,RuntimeError):lease={'unavailable':True}
                with lock:
                    state['requests']+=1;state['by_route'][replica]=state['by_route'].get(replica,0)+1
                    mode=state['mode'];delay=state['delay']
                    if search:
                        state['active_searches']+=1;state['maximum_searches']=max(state['maximum_searches'],state['active_searches'])
                    state['events'].append({'at':time.time(),'route':replica,'method':self.command,'path':path.split('?')[0],'phase':'start','lease':lease})
                    state['events']=state['events'][-4000:]
                try:
                    if search and delay:time.sleep(delay)
                    if mode=='fail_all' or (mode=='fail_search' and search):
                        with lock:state['failures']+=1
                        self.send_json(503,{'error':'isolated acceptance injected outage'});return
                    headers={k:v for k,v in self.headers.items() if k.lower() in ['content-type','authorization']}
                    req=urllib.request.Request(qa.settings['meili_url']+path,data=body,method=self.command,headers=headers)
                    try:response=OPENER.open(req,timeout=12)
                    except urllib.error.HTTPError as error:response=error
                    with response:
                        data=response.read(16*1024*1024+1)
                        if len(data)>16*1024*1024:raise RuntimeError('proxy response limit')
                        self.send_response(response.status);self.send_header('Content-Type',response.headers.get('Content-Type','application/json'));self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                except (OSError,RuntimeError):
                    try:self.send_json(502,{'error':'isolated upstream unavailable'})
                    except OSError:pass
                finally:
                    with lock:
                        if search:state['active_searches']-=1
                        state['events'].append({'at':time.time(),'route':replica,'method':self.command,'path':path.split('?')[0],'phase':'end'})
        server=http_server.ThreadingHTTPServer(('127.0.0.1',self.settings['proxy_port']),Handler)
        server.daemon_threads=True
        print('Loopback proxy ready',flush=True);server.serve_forever()

    def coordination(self):
        evidence={}
        self.stop();self.stop(peer=True)
        try:
            for peer in [False,True]:
                started=time.time();self.start(peer=peer);deadline=time.monotonic()+120
                while True:
                    stats=self.proxy_stats()
                    events=[e for e in stats['events'] if e['at']>=started and e['phase']=='start' and e['path'].endswith('/search') and e.get('lease',{}).get('owner')]
                    if events:break
                    if time.monotonic()>=deadline:raise AssertionError('Isolated replica did not exercise real shared Redis lease')
                    time.sleep(.2)
                name='peer' if peer else 'primary'
                evidence[name]={'pid':int((self.root/(name+'.pid')).read_text()),'searches_with_lease':len(events),'lease_owners':sorted({e['lease']['owner'] for e in events})}
                self.stop(peer=peer)
        finally:
            self.stop();self.stop(peer=True);self.start();self.start(peer=True)
        stats=self.proxy_stats();assert stats['maximum_searches']<=2
        self.record('real-two-process-coordination',{'replica_exercises':evidence,'maximum_concurrent_meili_searches':stats['maximum_searches'],'shared_redis_db':self.settings['redis_db'],'note':'Each PID exercised the real lease during its isolated phase; both PIDs served alternating requests during the preceding 5-minute load. Proxy URL routes are not replica identities.'})

    def deployment_policy(self):
        original=self.settings['meili_route']
        page=self.api('/api/v1/discovery/statuses?limit=5')
        assert page['mode']=='meili_lexical' and page['next_cursor']
        try:
            self.stop();self.stop(peer=True)
            self.settings['meili_route']='b' if original=='a' else 'a';dump(self.settings_path,self.settings)
            self.start();self.start(peer=True)
            for peer in [False,True]:
                self.api('/api/v1/discovery/statuses?cursor='+urllib.parse.quote(page['next_cursor']),expected=409,peer=peer)
            self.record('deployment-policy-change',{'changed':'Meili connection URL, same real server and corpus','old_cursor_status_primary':409,'old_cursor_status_peer':409,'model_tested':False})
        finally:
            self.stop();self.stop(peer=True)
            self.settings['meili_route']=original;dump(self.settings_path,self.settings)
            self.start();self.start(peer=True)
        self.check_mode('meili_lexical')

    def search_fault(self):
        healthy=self.check_mode('meili_lexical')
        before=self.proxy_stats()['failures']
        try:
            self.proxy_control('fail_search')
            self.stop();self.start()
            failed=self.wait(lambda x:bool(x['index'].get('last_query_error')),timeout=80,label='shared search failure')
            peer=self.api('/api/v1/admin/discovery/status',peer=True)
            assert peer['index'].get('last_query_error')==failed['index']['last_query_error']
            assert peer['index'].get('last_query_error_at')==failed['index']['last_query_error_at']
            assert self.proxy_stats()['failures']>before,'The real proxy must observe a failed request'
            for peer_flag in [False,True]:
                feed=self.api('/api/v1/discovery/statuses?limit=20',peer=peer_flag)
                assert feed['mode']=='rules',feed['mode']
            self.record('real-search-failure',{'primary_error':failed['index']['last_query_error'],'peer_error':peer['index']['last_query_error'],'shared_timestamp':failed['index']['last_query_error_at'],'new_feed_mode':'rules','injected_failed_requests':self.proxy_stats()['failures']-before,'scope':'Real Meilisearch HTTP search outage; not a semantic model outage.'})
        finally:self.proxy_control('normal')
        self.stop();self.start()
        self.check_mode('meili_lexical')
        final=self.api('/api/v1/admin/discovery/status')
        assert not final['index'].get('last_query_error')
        assert not self.api('/api/v1/admin/discovery/status',peer=True)['index'].get('last_query_error')
        self.record('real-search-recovery',{'mode':'meili_lexical','shared_error_cleared':True})

    def load(self, duration, rate):
        # Fixed offered rate, no unbounded task queue. At most eight in flight.
        expected_mode=self.api('/api/v1/discovery/statuses?limit=20')['mode']
        if expected_mode not in ['meili_lexical','meili_hybrid']:
            raise AssertionError('Enhanced feed must be ready before load')
        samples=[];resources=[];missed=0;guard=threading.Lock();slots=threading.BoundedSemaphore(8)
        begin=time.monotonic();end=begin+duration;planned=math.ceil(duration*rate)
        def run(number):
            started=time.monotonic()
            try:
                status,_,body=http('GET',('http://127.0.0.1:'+str(self.settings['peer_port']) if number%2 else self.base)+'/api/v1/discovery/statuses?limit=20',token=self.fixture['accounts'][0]['token'],timeout=10)
                row={'status':status,'mode':body.get('mode'),'items':len(body.get('items',[])),'ms':(time.monotonic()-started)*1000}
            except Exception as error: row={'status':0,'error':type(error).__name__,'ms':(time.monotonic()-started)*1000}
            with guard:samples.append(row)
            slots.release()
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            number=0;next_resource=begin
            while time.monotonic()<end:
                if time.monotonic()>=next_resource:
                    resources.append(self.resources());next_resource+=5
                due=begin+number/rate;number+=1
                if due>=end:break
                time.sleep(max(0,due-time.monotonic()))
                if slots.acquire(blocking=False):executor.submit(run,number)
                else:missed+=1
        dump(self.root/'stable-load-samples.json',samples)
        resources.append(self.resources());dump(self.root/'stable-load-resources.json',resources)
        elapsed=time.monotonic()-begin
        unscheduled=max(0,planned-len(samples)-missed)
        ordered=sorted(x['ms'] for x in samples)
        quantile=lambda n:round(ordered[min(len(ordered)-1,int(len(ordered)*n))],2)
        modes={x.get('mode') for x in samples};errors=sum(x['status']!=200 or x.get('items',0)!=20 for x in samples)
        self.record('stable-load',{'duration_seconds':duration,'offered_rps':rate,'planned':planned,'completed':len(samples),'missed_due_to_inflight_cap':missed,'unscheduled':unscheduled,'elapsed_seconds':round(elapsed,3),'errors':errors,'modes':sorted(str(x) for x in modes),'p50_ms':quantile(.5),'p95_ms':quantile(.95),'p99_ms':quantile(.99),'maximum_ms':quantile(1),'authors':len(self.fixture['accounts']),'statuses':len(self.fixture['statuses']),'notes':'Synthetic small-scale load; not the million-status production acceptance gate.'})
        assert errors==0 and missed==0 and unscheduled==0 and len(samples)==planned
        assert modes=={expected_mode},'Unexpected fallback during healthy enhanced load'


def parser():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=['prepare','start','stop','seed','status','approve','lexical','hybrid','privacy','revocation','load','proxy','proxy-start','proxy-stats','proxy-control','proxy-stop','search-fault','deployment-policy','coordination','configure-model','embedding-proxy','embedding-proxy-start','embedding-proxy-stop'])
    p.add_argument('--model-file',type=Path)
    p.add_argument('--runtime',type=Path,default=DEFAULT_RUNTIME)
    for key,value in DEFAULTS.items():p.add_argument('--'+key.replace('_','-'),dest=key,type=int if isinstance(value,int) else str)
    p.add_argument('--fault-mode',choices=['normal','fail_search','fail_all'],default='normal');p.add_argument('--delay',type=float,default=0)
    p.add_argument('--peer',action='store_true');p.add_argument('--duration',type=int,default=300);p.add_argument('--rate',type=float,default=4)
    return p


def main():
    args=parser().parse_args()
    if not 1<=args.duration<=900 or not 0.1<=args.rate<=20:
        raise ValueError('QA duration must be 1..900 seconds and offered rate 0.1..20 RPS')
    qa=Acceptance(args)
    if args.action not in ['prepare','proxy','embedding-proxy','stop','proxy-stop','embedding-proxy-stop']:
        qa.verify_ownership()
    if args.action=='prepare':qa.prepare()
    elif args.action=='start':qa.start(args.peer)
    elif args.action=='stop':qa.stop(args.peer)
    elif args.action=='seed':qa.seed()
    elif args.action=='status':print(json.dumps(qa.api('/api/v1/admin/discovery/status'),indent=2))
    elif args.action=='approve':print('Approved QA index version '+qa.approve())
    elif args.action in ['lexical','hybrid']:qa.approve();qa.check_mode('meili_'+args.action)
    elif args.action=='privacy':qa.privacy()
    elif args.action=='revocation':qa.revocation()
    elif args.action=='load':qa.load(args.duration,args.rate)
    elif args.action=='search-fault':qa.search_fault()
    elif args.action=='deployment-policy':qa.deployment_policy()
    elif args.action=='coordination':qa.coordination()
    elif args.action=='proxy':qa.proxy_serve()
    elif args.action=='proxy-start':qa.proxy_start()
    elif args.action=='proxy-stop':qa.stop(proxy=True)
    elif args.action=='proxy-control':print(json.dumps(qa.proxy_control(args.fault_mode,args.delay)))
    elif args.action=='proxy-stats':qa.record('proxy-stats',qa.proxy_stats())
    elif args.action=='configure-model':qa.configure_model(args.model_file)
    elif args.action=='embedding-proxy':qa.embedding_proxy()
    elif args.action=='embedding-proxy-start':qa.proxy_start(embedding=True)
    elif args.action=='embedding-proxy-stop':qa.stop(embedding=True)


if __name__=='__main__':main()
