// One-off codemod: moves hardcoded Chinese UI strings into src/locales/zh-CN.json.
import fs from 'node:fs'
import path from 'node:path'
import ts from 'typescript'
import { parse as parseSfc } from '@vue/compiler-sfc'

const ROOT = path.resolve('src')
const HAN = /[\u3400-\u9fff\uff01-\uff5e\u3000-\u303f]/
const HANCORE = /[\u3400-\u9fff]/
const DRY = process.argv.includes('--dry')
const messages = {} // ns -> {key: msg}
const byNs = {}

function walk(d, out = []) {
  for (const f of fs.readdirSync(d)) {
    const p = path.join(d, f)
    if (fs.statSync(p).isDirectory()) { if (f !== '__tests__' && f !== 'locales' && f !== 'i18n') walk(p, out) }
    else if (/\.(vue|ts)$/.test(f) && !/\.(spec|test)\.ts$/.test(f) && !f.endsWith('.d.ts')) out.push(p)
  }
  return out
}

function nsFor(file) {
  const rel = path.relative(ROOT, file).replace(/\\/g, '/')
  const base = path.basename(rel).replace(/\.(vue|ts)$/, '').replace(/[^A-Za-z0-9]/g, '_')
  const m = rel.match(/^domains\/([^/]+)\//)
  return (m ? m[1] : 'shared') + '.' + base
}

function esc(s) { return s.replace(/[{}@$|]/g, c => `{'${c}'}`) }

function keyFor(ns, msg) {
  const tbl = (messages[ns] ||= {})
  for (const [k, v] of Object.entries(tbl)) if (v === msg) return `${ns}.${k}`
  const k = 's' + (Object.keys(tbl).length + 1)
  tbl[k] = msg
  return `${ns}.${k}`
}

function call(fn, key, args) {
  return args.length ? `${fn}('${key}', [${args.join(', ')}])` : `${fn}('${key}')`
}

function skipNode(node) {
  for (let p = node.parent, c = node; p; c = p, p = p.parent) {
    if (ts.isImportDeclaration(p) || ts.isExportDeclaration(p) || ts.isLiteralTypeNode(p)) return true
    if ((ts.isPropertyAssignment(p) || ts.isPropertyDeclaration(p) || ts.isPropertySignature(p)) && p.name === c) return true
    if (ts.isCallExpression(p)) {
      const e = p.expression.getText()
      if (/^console\./.test(e)) return true
    }
    if (ts.isTypeNode(p)) return true
  }
  return false
}

// Returns edits [start,end,text] for TS code (offsets relative to code).
function tsEdits(code, ns, fn, isExpr) {
  const src = ts.createSourceFile('x.ts', isExpr ? `(${code})` : code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const shift = isExpr ? -1 : 0
  const edits = []
  const visit = (node) => {
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && HANCORE.test(node.text) && !skipNode(node)) {
      const key = keyFor(ns, esc(node.text))
      edits.push([node.getStart() + shift, node.getEnd() + shift, call(fn, key, [])])
      return
    }
    if (ts.isTemplateExpression(node) && [node.head, ...node.templateSpans.map(s => s.literal)].some(p => HANCORE.test(p.text)) && !skipNode(node)) {
      let msg = esc(node.head.text); const args = []
      node.templateSpans.forEach((s, i) => {
        const inner = s.expression.getText()
        const sub = tsEdits(inner, ns, fn, true)
        args.push(applyEdits(inner, sub))
        msg += `{${i}}` + esc(s.literal.text)
      })
      edits.push([node.getStart() + shift, node.getEnd() + shift, call(fn, keyFor(ns, msg), args)])
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(src)
  return edits
}

function applyEdits(s, edits) {
  edits.sort((a, b) => b[0] - a[0])
  for (const [a, b, t] of edits) s = s.slice(0, a) + t + s.slice(b)
  return s
}

const unhandled = []
function templateEdits(ast, ns) {
  const edits = []
  const exprEdit = (exp) => {
    const code = exp.content
    if (!HANCORE.test(code)) return
    const e = tsEdits(code, ns, '$t', true)
    const base = exp.loc.start.offset
    for (const [a, b, t] of e) edits.push([base + a, base + b, t])
  }
  const visit = (node) => {
    if (node.type === 1) {
      for (const p of node.props) {
        if (p.type === 6 && p.value && HANCORE.test(p.value.content)) {
          edits.push([p.loc.start.offset, p.loc.end.offset, `:${p.name}="${call('$t', keyFor(ns, esc(p.value.content.replace(/\s+/g, ' ').trim())), [])}"`])
        } else if (p.type === 7 && p.exp && p.name !== 'for') exprEdit(p.exp)
        else if (p.type === 7 && p.name === 'for' && p.exp && HANCORE.test(p.exp.content)) unhandled.push(ns + ' v-for: ' + p.exp.content)
      }
    }
    if (node.type === 5) { /* handled in groups */ }
    const kids = node.children || []
    let i = 0
    while (i < kids.length) {
      if (kids[i].type !== 2 && kids[i].type !== 5) { visit(kids[i]); i++; continue }
      let j = i
      while (j < kids.length && (kids[j].type === 2 || kids[j].type === 5)) j++
      const group = kids.slice(i, j)
      if (group.some(n => n.type === 2 && HANCORE.test(n.content))) {
        let msg = ''; const args = []
        for (const n of group) {
          if (n.type === 2) msg += esc(n.content.replace(/\s+/g, ' '))
          else {
            const code = n.content.content
            const sub = HANCORE.test(code) ? tsEdits(code, ns, '$t', true) : []
            args.push(applyEdits(code, sub).trim())
            msg += `{${args.length - 1}}`
          }
        }
        const lead = msg.match(/^\s*/)[0].length, trail = msg.match(/\s*$/)[0].length
        msg = msg.trim()
        const start = group[0].loc.start.offset, end = group[group.length - 1].loc.end.offset
        const raw = group.map(n => n.loc.source).join('')
        const rawLead = raw.match(/^\s*/)[0], rawTrail = raw.match(/\s*$/)[0]
        const argsStr = args.map(a => a.includes('"') ? a : a)
        edits.push([start, end, rawLead + `{{ ${call('$t', keyFor(ns, msg), argsStr)} }}` + rawTrail])
        void lead; void trail
      } else group.forEach(n => { if (n.type === 5) exprEdit(n.content) })
      i = j
    }
  }
  visit(ast)
  return edits
}

function ensureImport(code, isSetupOrTs) {
  if (/import \{[^}]*\bt\b[^}]*\} from '@\/shared\/i18n'/.test(code)) return code
  const line = `import { t } from '@/shared/i18n'\n`
  // place after last import
  const re = /^import[\s\S]*?from\s+['"][^'"]+['"]\s*;?\s*$/gm
  let last = -1, m
  while ((m = re.exec(code))) last = m.index + m[0].length
  if (last >= 0) return code.slice(0, last) + '\n' + line.trimEnd() + code.slice(last)
  return line + code
}

let changed = 0
for (const file of walk(ROOT)) {
  let src = fs.readFileSync(file, 'utf8')
  if (!HANCORE.test(src)) continue
  const ns = nsFor(file)
  let out
  if (file.endsWith('.ts')) {
    const e = tsEdits(src, ns, 't', false)
    if (!e.length) continue
    out = ensureImport(applyEdits(src, e))
  } else {
    const { descriptor, errors } = parseSfc(src, { filename: file })
    if (errors.length) { console.error('PARSE', file, errors[0]); continue }
    const edits = []
    if (descriptor.template) {
      const te = templateEdits(descriptor.template.ast, ns)
      // template ast offsets are relative to the whole file in compiler-sfc v3.4+
      edits.push(...te)
    }
    const scriptEdits = []
    for (const blk of [descriptor.script, descriptor.scriptSetup].filter(Boolean)) {
      const e = tsEdits(blk.content, ns, 't', false)
      if (e.length) scriptEdits.push({ blk, e })
    }
    if (!edits.length && !scriptEdits.length) continue
    // Apply script edits including import inside block content
    for (const { blk, e } of scriptEdits) {
      const newContent = ensureImport(applyEdits(blk.content, e))
      edits.push([blk.loc.start.offset, blk.loc.end.offset, newContent])
    }
    out = applyEdits(src, edits)
  }
  changed++
  if (!DRY) fs.writeFileSync(file, out)
}
fs.mkdirSync('src/locales', { recursive: true })
const nested = {}
for (const [ns, tbl] of Object.entries(messages)) {
  const [a, b] = ns.split('.')
  ;(nested[a] ||= {})[b] = tbl
}
fs.writeFileSync('src/locales/zh-CN.json', JSON.stringify(nested, null, 2) + '\n')
console.log('files', changed, 'messages', Object.values(messages).reduce((s, t) => s + Object.keys(t).length, 0))
console.log('unhandled', unhandled)
