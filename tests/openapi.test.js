import assert from 'node:assert';
import test from 'node:test';
import { getOpenAPISpec, renderSwaggerUIHTML, openapiHandler } from '../src/middleware/openapi.js';

test('getOpenAPISpec should return valid OpenAPI 3.1 specification object', () => {
  const spec = getOpenAPISpec();
  assert.strictEqual(typeof spec, 'object');
  assert.strictEqual(spec.openapi, '3.1.0');
  assert.strictEqual(typeof spec.paths, 'object');
  assert.ok(spec.paths['/health']);
  assert.ok(spec.paths['/circles']);
});

test('renderSwaggerUIHTML should return HTML string with Embedded Swagger UI', () => {
  const html = renderSwaggerUIHTML();
  assert.strictEqual(typeof html, 'string');
  assert.ok(html.includes('swagger-ui'));
  assert.ok(html.includes('SwaggerUIBundle'));
});

test('openapiHandler should handle JSON and HTML requests', () => {
  let jsonSent = null;
  let htmlSent = null;

  const jsonRes = {
    type: (t) => jsonRes,
    send: (payload) => { jsonSent = payload; return jsonRes; }
  };
  openapiHandler({ url: '/api-docs/openapi.json' }, jsonRes);
  assert.strictEqual(jsonSent.openapi, '3.1.0');

  const htmlRes = {
    type: (t) => htmlRes,
    send: (payload) => { htmlSent = payload; return htmlRes; }
  };
  openapiHandler({ url: '/api-docs' }, htmlRes);
  assert.ok(htmlSent.includes('<!DOCTYPE html>'));
});
