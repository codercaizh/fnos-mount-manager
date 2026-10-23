#!/usr/bin/env node
// Generate the bundled SDK from pinned upstream npm dependency instead of
// committing the third-party minified distribution as original source.
const fs = require('node:fs');
const path = require('node:path');
const source = path.join(path.dirname(path.dirname(require.resolve('@trimjs/web-app'))), 'dist', 'index.js');
const destination = path.join(__dirname, '../cmd/mountmgr/web/fnos-sdk.js');
fs.mkdirSync(path.dirname(destination), { recursive: true });
fs.copyFileSync(source, destination);
console.log('Prepared fnOS SDK from pinned @trimjs/web-app dependency.');
