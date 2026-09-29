// Copies the built client into the Go embed package.
//
// Go embeds only files below the embedding file, so the Go side cannot reference
// frontend/dist directly. This step is what makes `//go:embed all:dist` in
// backend/internal/webdist see the current bundle.
//
// The destination placeholder is preserved: it is what keeps the embed pattern
// valid before the first build.

import { cp, mkdir, readdir, rm } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = resolve(here, '..', 'dist')
const target = resolve(here, '..', '..', 'backend', 'internal', 'webdist', 'dist')

const keep = new Set(['.gitkeep'])

await mkdir(target, { recursive: true })
for (const entry of await readdir(target)) {
  if (keep.has(entry)) {
    continue
  }
  await rm(join(target, entry), { recursive: true, force: true })
}
await cp(source, target, { recursive: true })

const copied = (await readdir(target)).filter((entry) => !keep.has(entry))
console.log(`embed-dist: copied ${copied.length} entries into ${target}`)
