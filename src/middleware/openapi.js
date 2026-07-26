import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const openapiPath = path.resolve(__dirname, '../openapi/openapi3.json');

let openapiSpec = null;
try {
  const content = fs.readFileSync(openapiPath, 'utf8');
  openapiSpec = JSON.parse(content);
} catch (err) {
  openapiSpec = { openapi: '3.1.0', info: { title: 'Moistello API', version: '1.0.0' } };
}

export function getOpenAPISpec() {
  return openapiSpec;
}

export function renderSwaggerUIHTML(spec = openapiSpec) {
  const specJsonStr = JSON.stringify(spec);
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Moistello API Documentation - Swagger UI</title>
  <link rel="stylesheet" type="text/css" href="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.11.0/swagger-ui.min.css" />
  <style>
    html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; background: #fafafa; }
    .swagger-ui .topbar { display: none; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.11.0/swagger-ui-bundle.js"></script>
  <script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.11.0/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        spec: ${specJsonStr},
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        plugins: [
          SwaggerUIBundle.plugins.DownloadUrl
        ],
        layout: "BaseLayout"
      });
    };
  </script>
</body>
</html>`;
}

export function openapiHandler(req, res) {
  if (req.url && req.url.endsWith('/openapi.json')) {
    if (res && typeof res.type === 'function') {
      return res.type('application/json').send(openapiSpec);
    }
    if (res && typeof res.setHeader === 'function') {
      res.setHeader('Content-Type', 'application/json');
      return res.end(JSON.stringify(openapiSpec));
    }
  }

  const html = renderSwaggerUIHTML();
  if (res && typeof res.type === 'function') {
    return res.type('text/html').send(html);
  }
  if (res && typeof res.setHeader === 'function') {
    res.setHeader('Content-Type', 'text/html');
    return res.end(html);
  }
}

export default openapiHandler;
