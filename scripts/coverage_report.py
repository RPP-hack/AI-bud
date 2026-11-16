import json
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / 'docs/coverage'
OUTPUT.mkdir(parents=True, exist_ok=True)
components = {}
for name, path in [('backend', ROOT / 'backend/services/api/coverage.xml'),
                   ('rag', OUTPUT / 'python.xml')]:
    tree = ET.parse(path).getroot()
    files = {}
    for cls in tree.findall('.//class'):
        lines = files.setdefault(cls.attrib['filename'], {})
        for line in cls.findall('./lines/line'):
            number = int(line.attrib['number'])
            lines[number] = max(lines.get(number, 0), int(line.attrib['hits']))
    valid = sum(len(lines) for lines in files.values())
    covered = sum(sum(hits > 0 for hits in lines.values()) for lines in files.values())
    components[name] = {'covered': covered, 'total': valid, 'files': sorted(files)}
    target = OUTPUT / 'backend.xml'
    if name == 'backend':
        tree.attrib.pop('timestamp', None)
        for source in tree.findall('./sources/source'):
            source.text = 'backend/services/api'
        ET.indent(tree)
        ET.ElementTree(tree).write(target, encoding='utf-8', xml_declaration=True)

frontend = json.loads((ROOT / 'frontend/coverage/coverage-summary.json').read_text())
lines = frontend['total']['lines']
components['frontend'] = {'covered': lines['covered'], 'total': lines['total'],
                          'files': sorted('frontend/' + str(Path(name).relative_to(ROOT / 'frontend'))
                                          for name in frontend if name != 'total')}
for component in components.values():
    if not component['total']:
        raise SystemExit('Empty component coverage')
    component['percent'] = round(100 * component['covered'] / component['total'], 2)
covered = sum(c['covered'] for c in components.values())
total = sum(c['total'] for c in components.values())
report = {'metric': 'executable line coverage', 'components': components,
          'total': {'covered': covered, 'total': total, 'percent': round(100 * covered / total, 2)},
          'exclusions': ['dependencies', 'tests', 'generated protobuf'],
          'tools': {'backend': 'go test + gocover-cobertura v1.5.0', 'rag': 'coverage.py', 'frontend': 'Vitest V8'}}
(OUTPUT / 'summary.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
rows = ['# Покрытие строк приложения', '', '| Компонент | Покрыто | Исполняемых строк | Покрытие |', '| --- | ---: | ---: | ---: |']
for name, component in [*components.items(), ('Всего', report['total'])]:
    rows.append(f"| {name} | {component['covered']} | {component['total']} | {component['percent']}% |")
rows += ['', 'Проценты не усредняются: итог рассчитан по сумме покрытых и исполняемых строк.',
         'Методика, исключения и команды воспроизведения: [COVERAGE.md](../COVERAGE.md).', '']
(OUTPUT / 'SUMMARY.md').write_text('\n'.join(rows))
print(json.dumps(report['total']))
if covered / total <= .8:
    sys.exit('Executable line coverage must exceed 80%')
