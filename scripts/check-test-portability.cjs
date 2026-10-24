const fs = require('node:fs');
const path = require('node:path');

// CI runs in a fresh checkout, not on the developer's build machine.
const scripts = path.resolve(__dirname);
const localImport = /require\s*\(\s*['"]\/(?:data|home|root|tmp|mnt)\//;
let failures = 0;
for (const file of fs.readdirSync(scripts).filter(name => name.endsWith('.cjs'))) {
  const code = fs.readFileSync(path.join(scripts, file), 'utf8');
  if (localImport.test(code)) {
    console.error(`Machine-specific absolute require() in scripts/${file}`);
    failures++;
  }
}
if (failures) process.exit(1);
console.log('Portable test dependencies verified');
