import { defineConfig } from '@playwright/test'
import { dataDir } from './e2e/paths'

// Runs the real binary on a fresh data directory, so the sweep starts at the setup wizard.
const port = 18380
// The same command on Linux, macOS and Windows: node empties the data directory, then the program starts.
const program = process.platform === 'win32' ? '..\\bin\\bitvoice.exe' : '../bin/bitvoice'
const fresh = `node -e "require('fs').rmSync(process.argv[1], { recursive: true, force: true })" "${dataDir}"`
export default defineConfig({
  testDir: 'e2e',
  timeout: 180_000,
  workers: 1,
  // Files run in name order on one server: the sweep must meet the setup wizard, so it comes before
  // anything that signs in, and the profiles run in a project of their own after everything else.
  projects: [
    { name: 'main', testMatch: ['shots.spec.ts', 'sweep.spec.ts', 'two-step.spec.ts', 'webrtc.spec.ts'] },
    // After the sweep, which must meet the setup wizard on a fresh server.
    { name: 'features', testMatch: ['calls.spec.ts', 'features.spec.ts', 'judges.spec.ts'], dependencies: ['main'] },
    { name: 'profiles', testMatch: ['profiles.spec.ts'], dependencies: ['features'] },
  ],
  // HTTPS only; the fresh server's certificate is its own self-signed one.
  use: { baseURL: `https://127.0.0.1:${port}`, ignoreHTTPSErrors: true },
  webServer: {
    command: `${fresh} && ${program} -data "${dataDir}" -no-browser -https 127.0.0.1:${port} -sip 127.0.0.1:25060`,
    url: `https://127.0.0.1:${port}/api/health/live`,
    ignoreHTTPSErrors: true,
    reuseExistingServer: false,
    // No whisper-server for the test server, even where one is installed: its state is shown, nothing is fetched.
    env: { BITVOICE_META_GRAPH: 'http://127.0.0.1:18399', BITVOICE_WHISPER_SERVER: '/nonexistent/whisper-server' },
  },
})
