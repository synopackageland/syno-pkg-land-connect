// Connect's real-NAS e2e: `npm run e2e:nas` opens the SSH tunnel and sets DSM_E2E_URL.
import type {E2EConfig} from 'e2e';
import {dsmTarget, magpieAgent} from '@synopackageland/cli/e2e';

export default {
  tests: ['tests/e2e/**/*.e2e.ts'],
  targets: [dsmTarget()],
  workers: 1,
  retries: 0,
  timeout: 600_000,
  actionTimeout: 15_000,
  trace: 'retain-on-failure',
  output: '.e2e/output',
  agents: {default: {...magpieAgent(), maxModelCalls: 30, maxSteps: 40}},
} satisfies E2EConfig;
