import assert from 'node:assert';
import test from 'node:test';
import { rateLimitMiddleware } from '../src/middleware/rateLimiter.js';
import redisService from '../src/services/redisService.js';

test('rateLimitMiddleware denies requests when Redis is unreachable', async () => {
  let statusCode = 0;
  let sentJson = null;
  const req = { ip: '127.0.0.1', headers: {} };
  const res = {
    status: (code) => {
      statusCode = code;
      return res;
    },
    json: (data) => {
      sentJson = data;
      return res;
    },
  };
  const next = () => { assert.fail('next should not be called'); };

  redisService.client = null; // Unreachable
  await rateLimitMiddleware(req, res, next);

  assert.strictEqual(statusCode, 503);
  assert.strictEqual(sentJson.error, 'Service Temporarily Unavailable');
});