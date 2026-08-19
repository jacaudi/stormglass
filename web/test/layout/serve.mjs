// Static server for a built web/dist plus the recorded API fixtures, mirroring
// internal/httpserver/server.go:164-200 exactly:
//   - /api/* never falls back to the SPA index
//   - a path resolving to a real file is served
//   - a missing path WITH a file extension 404s
//   - a missing path WITHOUT one serves index.html
// Zero dependencies: node's own http/fs only.
import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { dirname, extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = join(HERE, '..', '..', 'dist');
const FIXTURES = join(HERE, 'fixtures');

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.map': 'application/json; charset=utf-8',
  '.pmtiles': 'application/octet-stream',
};

function fixture(name) {
  return JSON.parse(readFileSync(join(FIXTURES, name), 'utf8'));
}

function apiBody(pathname, rain) {
  if (pathname === '/api/capabilities') return fixture('capabilities.json');
  if (pathname === '/api/station') return fixture('station.json');
  if (pathname === '/api/almanac') return fixture('almanac.json');
  if (pathname === '/api/observations/current') {
    return fixture(rain ? 'observations-current.rain.json' : 'observations-current.json');
  }
  if (pathname === '/api/observations/summary') return fixture('observations-summary.json');
  if (/^\/api\/radar\/[A-Z]{3}$/.test(pathname)) return fixture('radar.json');
  return null;
}

export async function startServer({ port = 0, rain = false } = {}) {
  const log = [];
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    const pathname = url.pathname;
    const record = (status) => log.push({ method: req.method, path: pathname, status });

    if (pathname.startsWith('/api/')) {
      const body = apiBody(pathname, rain);
      if (body === null) {
        record(404);
        // Go's http.NotFound writes a trailing newline; parity is asserted on
        // STATUS, and nothing reads the body, so the difference is cosmetic.
        res.writeHead(404).end('404 page not found\n');
        return;
      }
      record(200);
      res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify(body));
      return;
    }

    let assetPath = normalize(pathname).replace(/^(\.\.[/\\])+/, '').replace(/^\/+/, '');
    if (assetPath === '' || assetPath === '.') assetPath = 'index.html';

    if (assetPath !== 'index.html') {
      try {
        const st = await stat(join(DIST, assetPath));
        if (st.isFile()) {
          record(200);
          res.writeHead(200, {
            'Content-Type': TYPES[extname(assetPath)] ?? 'application/octet-stream',
            'Cache-Control': 'public, max-age=31536000, immutable',
          });
          res.end(await readFile(join(DIST, assetPath)));
          return;
        }
      } catch {
        /* fall through to the extension rule below */
      }
      if (extname(assetPath) !== '') {
        record(404);
        res.writeHead(404).end('404 page not found');
        return;
      }
    }

    record(200);
    res.writeHead(200, { 'Content-Type': TYPES['.html'], 'Cache-Control': 'no-cache' });
    res.end(await readFile(join(DIST, 'index.html')));
  });

  await new Promise((resolve) => server.listen(port, '127.0.0.1', resolve));
  const actual = server.address().port;
  return {
    port: actual,
    log,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

// Standalone: `node serve.mjs` (PORT and RAIN env vars) for eyeballing a build.
if (import.meta.url === `file://${process.argv[1]}`) {
  const { port } = await startServer({
    port: Number(process.env.PORT ?? 4319),
    rain: process.env.RAIN === '1',
  });
  console.log(`serving web/dist on http://127.0.0.1:${port}`);
}
