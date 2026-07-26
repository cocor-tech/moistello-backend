import assert from 'node:assert';
import test from 'node:test';
import { validateRequest, zodValidationPlugin } from '../src/middleware/validateZod.js';

test('validateRequest should call next() when validation succeeds', async () => {
  let nextCalled = false;
  const req = { body: { name: 'Alice' } };
  const res = {};
  const next = () => { nextCalled = true; };

  const bodySchema = {
    safeParse: (data) => ({ success: true, data }),
  };

  const middleware = validateRequest({ body: bodySchema });
  await middleware(req, res, next);

  assert.strictEqual(nextCalled, true);
});

test('validateRequest should return HTTP 400 with field-level details on failure', async () => {
  let statusCode = 0;
  let sentPayload = null;
  const req = { body: {} };
  const res = {
    code: (code) => {
      statusCode = code;
      return res;
    },
    send: (payload) => {
      sentPayload = payload;
      return res;
    },
  };
  const next = () => { assert.fail('next should not be called'); };

  const bodySchema = {
    safeParse: () => ({
      success: false,
      error: {
        errors: [
          { path: ['email'], message: 'Invalid email' },
        ],
      },
    }),
  };

  const middleware = validateRequest({ body: bodySchema });
  await middleware(req, res, next);

  assert.strictEqual(statusCode, 400);
  assert.strictEqual(sentPayload.error, 'Validation Error');
  assert.strictEqual(sentPayload.details[0].field, 'body.email');
});

test('zodValidationPlugin should decorate fastify instance', () => {
  let decoratedName = '';
  let decoratedFunc = null;
  let doneCalled = false;

  const fastify = {
    decorate: (name, func) => {
      decoratedName = name;
      decoratedFunc = func;
    },
  };
  const done = () => { doneCalled = true; };

  zodValidationPlugin(fastify, {}, done);
  assert.strictEqual(decoratedName, 'validateRequest');
  assert.strictEqual(decoratedFunc, validateRequest);
  assert.strictEqual(doneCalled, true);
});
