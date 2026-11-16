import { createServer } from 'node:http';
import { createReadStream } from 'node:fs';
import { resolve, dirname, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { resolveSiteFile } from './site-files.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../static-site');
const portIndex = process.argv.indexOf('--port');
const rawPort = portIndex < 0 ? '3000' : (process.argv[portIndex + 1] ?? '');
const port = Number(rawPort);
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error(`Invalid --port value: ${rawPort}`);
}
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp', '.ico': 'image/x-icon', '.woff2': 'font/woff2', '.txt': 'text/plain; charset=utf-8', '.mp4': 'video/mp4', '.srt': 'application/x-subrip; charset=utf-8' };
const server = createServer(async (request, response) => {
  try {
    if (!['GET', 'HEAD'].includes(request.method)) { response.writeHead(405, { Allow: 'GET, HEAD' }).end(); return; }
    const reqTarget = request.url || '/';
    if (reqTarget.startsWith('//')) { response.writeHead(400).end('Bad request'); return; }
    const url = new URL(reqTarget, 'http://localhost');
    if (url.pathname === '/docs') { response.writeHead(308, { Location: '/docs/' + url.search }).end(); return; }
    let file = await resolveSiteFile(root, url.pathname);
    const status = file ? 200 : 404;
    if (!file) file = await resolveSiteFile(root, url.pathname.startsWith('/docs/') ? '/docs/404.html' : '/404.html');
    if (!file) { response.writeHead(404).end('Not found'); return; }
    const ext = extname(file.path).toLowerCase();
    const contentType = Object.hasOwn(types, ext) ? types[ext] : 'application/octet-stream';
    response.writeHead(status, { 'Content-Type': contentType, 'Content-Length': file.info.size, 'Cache-Control': 'no-cache', 'X-Content-Type-Options': 'nosniff' });
    if (request.method === 'HEAD') response.end();
    else createReadStream(file.path).on('error', () => response.destroy()).pipe(response);
  } catch { if (!response.headersSent) response.writeHead(500).end('Unable to serve this page'); else response.destroy(); }
});
server.listen(port, '127.0.0.1', () => console.log(`GENYEE unified preview: http://127.0.0.1:${port}/`));
