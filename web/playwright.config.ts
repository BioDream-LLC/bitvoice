import { defineConfig } from '@playwright/test'

// Runs the real binary on a fresh data directory, so the sweep starts at the setup wizard.
const port = 18380
export default defineConfig({
  testDir: 'e2e',
  timeout: 180_000,
  workers: 1,
  // Files run in name order on one server: the sweep must meet the setup wizard, so it comes before
  // anything that signs in, and the profiles run in a project of their own after everything else.
  projects: [
    { name: 'main', testMatch: ['shots.spec.ts', 'sweep.spec.ts', 'two-step.spec.ts', 'webrtc.spec.ts'] },
    // After the sweep, which must meet the setup wizard on a fresh server.
    { name: 'features', testMatch: ['calls.spec.ts', 'features.spec.ts'], dependencies: ['main'] },
    { name: 'profiles', testMatch: ['profiles.spec.ts'], dependencies: ['features'] },
  ],
  // HTTPS only; the fresh server's certificate is its own self-signed one.
  use: { baseURL: `https://127.0.0.1:${port}`, ignoreHTTPSErrors: true },
  webServer: {
    command: `rm -rf /tmp/bitvoice-e2e && ../bin/bitvoice -data /tmp/bitvoice-e2e -no-browser -https 127.0.0.1:${port} -sip 127.0.0.1:25060`,
    url: `https://127.0.0.1:${port}/api/health/live`,
    ignoreHTTPSErrors: true,
    reuseExistingServer: false,
  },
})
