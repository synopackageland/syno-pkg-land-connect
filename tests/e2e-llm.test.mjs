// The e2e agent's model must reach magpie with reasoning effort max: read the
// request body a local stand-in provider receives. No NAS, no real model.
import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {generateText} from 'ai';
import {magpieAgent} from '@synopackageland/cli/e2e';

async function provider() {
  const bodies = [];
  const server = createServer((request, response) => {
    let body = '';
    request.on('data', chunk => { body += chunk; });
    request.on('end', () => {
      bodies.push({path: request.url, body: JSON.parse(body)});
      response.setHeader('Content-Type', 'application/json');
      response.end(JSON.stringify({id: 'x', object: 'chat.completion', created: 0, model: 'm',
        choices: [{index: 0, message: {role: 'assistant', content: 'ok'}, finish_reason: 'stop'}],
        usage: {prompt_tokens: 1, completion_tokens: 1, total_tokens: 2}}));
    });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return {bodies, url: `http://127.0.0.1:${server.address().port}/v1`, close: () => server.close()};
}

test('the agent model sends codex/gpt-6-luna with reasoning effort max to the configured endpoint', async () => {
  const fake = await provider();
  try {
    const agent = magpieAgent({E2E_LLM_BASE_URL: fake.url, E2E_LLM_API_KEY: 'test-provider-key', E2E_LLM_MODEL: 'codex/gpt-6-luna', E2E_LLM_REASONING_EFFORT: 'max'});
    await generateText({model: agent.model, providerOptions: agent.providerOptions, prompt: 'hi'});
    assert.equal(fake.bodies.length, 1);
    assert.equal(fake.bodies[0].path, '/v1/chat/completions');
    assert.equal(fake.bodies[0].body.model, 'codex/gpt-6-luna');
    assert.equal(fake.bodies[0].body.reasoning_effort, 'max');
  } finally { fake.close(); }
});

test('E2E_LLM_MODEL and E2E_LLM_REASONING_EFFORT select the configured model and effort', async () => {
  const fake = await provider();
  try {
    const agent = magpieAgent({E2E_LLM_BASE_URL: fake.url, E2E_LLM_API_KEY: 'test-provider-key', E2E_LLM_MODEL: 'codex/other', E2E_LLM_REASONING_EFFORT: 'high'});
    await generateText({model: agent.model, providerOptions: agent.providerOptions, prompt: 'hi'});
    assert.equal(fake.bodies[0].body.model, 'codex/other');
    assert.equal(fake.bodies[0].body.reasoning_effort, 'high');
  } finally { fake.close(); }
});

