import { spawnSync } from 'node:child_process';
import { readFileSync, rmSync, mkdtempSync } from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const target = new URL(process.env.E2E_TLS_PROXY_TARGET ?? 'http://127.0.0.1:1782');
const listenHost = process.env.E2E_TLS_PROXY_HOST ?? '127.0.0.1';
const listenPort = parsePort(process.env.E2E_TLS_PROXY_PORT ?? '1783');

if (target.protocol !== 'http:') {
  throw new Error('E2E_TLS_PROXY_TARGET must use http://');
}

const certificateDirectory = mkdtempSync(path.join(os.tmpdir(), 'fight-landlord-e2e-tls-'));
const keyFile = path.join(certificateDirectory, 'server.key');
const certificateFile = path.join(certificateDirectory, 'server.crt');
generateCertificate(keyFile, certificateFile);

const server = https.createServer({
  key: readFileSync(keyFile),
  cert: readFileSync(certificateFile)
}, proxyHTTP);

server.on('upgrade', proxyWebSocket);
server.on('clientError', (error, socket) => {
  console.error('TLS reverse proxy rejected a client connection', errorName(error));
  socket.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n');
});
server.on('error', (error) => {
  console.error('TLS reverse proxy failed', errorName(error));
  cleanup();
  process.exitCode = 1;
});
server.listen(listenPort, listenHost, () => {
  console.log(`TLS reverse proxy listening on https://${listenHost}:${listenPort}`);
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => {
    server.close((error) => {
      if (error) {
        console.error('TLS reverse proxy shutdown failed', errorName(error));
        process.exitCode = 1;
      }
      cleanup();
    });
  });
}

process.once('exit', cleanup);

function proxyHTTP(request, response) {
  const headers = forwardedHeaders(request.headers);
  const upstreamRequest = http.request({
    hostname: target.hostname,
    port: target.port || 80,
    method: request.method,
    path: request.url,
    headers
  }, (upstreamResponse) => {
    response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
    upstreamResponse.pipe(response);
  });

  upstreamRequest.on('error', (error) => {
    console.error('TLS reverse proxy upstream request failed', errorName(error));
    if (!response.headersSent) response.writeHead(502, { 'Content-Type': 'text/plain; charset=utf-8' });
    response.end('Bad Gateway');
  });
  request.on('aborted', () => upstreamRequest.destroy());
  request.pipe(upstreamRequest);
}

function proxyWebSocket(request, clientSocket, head) {
  const upstreamSocket = net.connect(Number(target.port || 80), target.hostname);
  let connected = false;

  upstreamSocket.once('connect', () => {
    connected = true;
    const headers = forwardedHeaders(request.headers);
    const lines = [`${request.method ?? 'GET'} ${request.url ?? '/'} HTTP/${request.httpVersion}`];
    for (const [name, value] of Object.entries(headers)) {
      if (value === undefined) continue;
      lines.push(`${name}: ${Array.isArray(value) ? value.join(', ') : value}`);
    }
    upstreamSocket.write(`${lines.join('\r\n')}\r\n\r\n`);
    if (head.length > 0) upstreamSocket.write(head);
    clientSocket.pipe(upstreamSocket).pipe(clientSocket);
  });

  upstreamSocket.once('error', (error) => {
    console.error('TLS reverse proxy WebSocket upstream failed', errorName(error));
    if (!connected && clientSocket.writable) {
      clientSocket.end('HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n');
    } else {
      clientSocket.destroy();
    }
  });
  clientSocket.once('error', (error) => {
    console.error('TLS reverse proxy WebSocket client failed', errorName(error));
    upstreamSocket.destroy();
  });
  clientSocket.once('close', () => upstreamSocket.destroy());
}

function forwardedHeaders(headers) {
  return {
    ...headers,
    host: target.host,
    'x-forwarded-host': headers.host ?? '',
    'x-forwarded-proto': 'https'
  };
}

function generateCertificate(keyPath, certPath) {
  const result = spawnSync('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-sha256', '-nodes',
    '-keyout', keyPath,
    '-out', certPath,
    '-days', '1',
    '-subj', '/CN=127.0.0.1',
    '-addext', 'subjectAltName=IP:127.0.0.1,DNS:localhost'
  ], { encoding: 'utf8' });
  if (result.error) {
    cleanup();
    throw result.error;
  }
  if (result.status !== 0) {
    cleanup();
    throw new Error(`openssl certificate generation failed with status ${result.status}: ${result.stderr.trim()}`);
  }
}

function cleanup() {
  rmSync(certificateDirectory, { force: true, recursive: true });
}

function errorName(error) {
  return error instanceof Error ? error.name : typeof error;
}

function parsePort(value) {
  const port = Number(value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('E2E_TLS_PROXY_PORT must be an integer between 1 and 65535');
  }
  return port;
}
