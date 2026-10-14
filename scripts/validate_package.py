"""Validate preparation files only; this is not a microservice integration test."""
from pathlib import Path
import json,xml.etree.ElementTree as ET,zipfile
root=Path(__file__).resolve().parents[1]
required=['README.md','START_HERE.html','prototype/index.html','presentation/Predefense_1.pptx','docs/01-proposal.md','docs/02-user-research.md','docs/03-literature-review.md','docs/04-system-comparison.md','docs/05-technology-comparison.md','docs/06-requirements.md','docs/07-architecture.md','docs/08-ux.md','docs/09-team-plan.md','docs/10-supervisor-review.md','docs/11-readiness.md','docs/12-references.md','docs/13-git-youtrack.md','tests/EVALUATION.md','tests/results.csv','planning/backlog.json']
for f in required:assert (root/f).is_file(),f'Missing {f}'
refs=json.loads((root/'docs/references.json').read_text());assert len(refs)>=12;assert sum(r['type']=='scientific' for r in refs)>=8
for p in root.rglob('*.json'):json.loads(p.read_text(encoding='utf-8'))
for name in ['01-context','02-use-cases','03-components']:
 for ext in ['svg','drawio']:ET.parse(root/f'diagrams/{name}.{ext}')
tasks=json.loads((root/'planning/backlog.json').read_text());ids={x['local_id'] for x in tasks};assert len(ids)==len(tasks)
for t in tasks:
 assert t['owner'] in ['Tamerlan','Aldiyar','Askhat'];assert all(d in ids for d in t['depends_on']);assert t['acceptance']
with zipfile.ZipFile(root/'presentation/Predefense_1.pptx') as z:assert z.testzip() is None
assert len((root/'tests/results.csv').read_text().splitlines())==1,'Do not fabricate results'
print(f'OK: required files, {len(refs)} sources, {len(tasks)} tasks, 3 diagram pairs and PPTX container. Backend not implemented.')
