import { createHash } from 'node:crypto'
import { readFileSync, readdirSync, mkdirSync, rmSync, copyFileSync, writeFileSync, existsSync } from 'node:fs'
import { dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = join(web, 'dist')
const target = join(web, 'bundle')
if (resolve(target) !== resolve(web, 'bundle') || !target.startsWith(web + sep)) throw new Error('Invalid bundle destination')
function files(root, relative = '') {
  return readdirSync(join(root, relative), { withFileTypes: true }).flatMap((entry) => {
    if (entry.isSymbolicLink()) throw new Error('Asset links are unsupported')
    const name = relative ? `${relative}/${entry.name}` : entry.name
    return entry.isDirectory() ? files(root, name) : [name]
  }).sort()
}
const names = files(source)
const manifest = { version: 1, files: Object.fromEntries(names.map((name) => [name, createHash('sha256').update(readFileSync(join(source, name))).digest('hex')])) }
const manifestText = JSON.stringify(manifest, null, 2) + '\n'
if (process.argv.includes('--check')) {
  if (!existsSync(join(target, 'manifest.json')) || readFileSync(join(target, 'manifest.json'), 'utf8') !== manifestText || files(target).filter((n) => n !== 'manifest.json').join('\n') !== names.join('\n') || names.some((name) => createHash('sha256').update(readFileSync(join(target, name))).digest('hex') !== manifest.files[name])) {
    throw new Error('Embedded bundle is stale; run npm run build in web')
  }
  console.log(`Embedded bundle verified: ${names.length} files`)
} else {
  // The destination is a fixed, resolved child of web, verified above.
  rmSync(target, { recursive: true, force: true })
  mkdirSync(target, { recursive: true })
  for (const name of names) {
    mkdirSync(dirname(join(target, name)), { recursive: true })
    copyFileSync(join(source, name), join(target, name))
  }
  writeFileSync(join(target, 'manifest.json'), manifestText)
  console.log(`Embedded bundle prepared: ${names.length} files`)
}
