import os from 'node:os'
import path from 'node:path'

/** The test server's data directory (made fresh for each run), in this system's temporary folder. */
export const dataDir = path.join(os.tmpdir(), 'bitvoice-e2e')
/** Where screenshots for the README go. */
export const shot = (name: string) => path.join(os.tmpdir(), `bv-shot-${name}.png`)
