"""Create issues only with --apply; credentials are read from environment."""
import argparse,json,os,pathlib,urllib.request,urllib.parse
p=argparse.ArgumentParser();p.add_argument('--apply',action='store_true');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[1]
tasks=json.loads((root/'planning/backlog.json').read_text(encoding='utf-8'))
project=os.environ.get('YOUTRACK_PROJECT_ID','PROJECT_ID_REQUIRED')
base=os.environ.get('YOUTRACK_URL','').rstrip('/')
token=os.environ.get('YOUTRACK_TOKEN','')
log=root/'private/youtrack_created.json'
created=json.loads(log.read_text()) if log.exists() else {}
if a.apply and (not token or project=='PROJECT_ID_REQUIRED' or urllib.parse.urlparse(base).scheme!='https'):
 p.error('Set HTTPS YOUTRACK_URL, YOUTRACK_PROJECT_ID and YOUTRACK_TOKEN.')
for t in tasks:
 if t['local_id'] in created: continue
 body={'project':{'id':project},'summary':t['summary'],'description':f"Local ID: {t['local_id']}\nProposed owner: {t['owner']}\nDue: {t['due_date']}\nPriority: {t['priority']}\nAcceptance: {t['acceptance']}\nDependencies: {', '.join(t['depends_on'])}\nAssign custom fields manually; draft requires team review."}
 if not a.apply:
  print(json.dumps(body,ensure_ascii=False));continue
 request=urllib.request.Request(base+'/api/issues?fields=id,idReadable',data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'},method='POST')
 try:
  with urllib.request.urlopen(request,timeout=30) as response: result=json.load(response)
 except Exception:
  raise SystemExit('Import stopped. Check server before retrying: the last issue may have been created.')
 created[t['local_id']]=result
 log.parent.mkdir(exist_ok=True)
 temp=log.with_suffix('.tmp');temp.write_text(json.dumps(created,indent=2));temp.replace(log)
 print(t['local_id'],result.get('idReadable',result['id']))
