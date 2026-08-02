import { resolve } from "node:path";

import { verifyV0WebWithBundledChromium } from "./v0-browser-verify.mjs";

const [targetURL, webSourceDirectory] = process.argv.slice(2);
if (!targetURL || !webSourceDirectory || process.argv.length !== 4) {
  throw new Error(
    "usage: node scripts/verify-v0-web-url.mjs <loopback-url> <web-source-directory>",
  );
}

await verifyV0WebWithBundledChromium(targetURL, resolve(webSourceDirectory));
process.stdout.write(`V0 packaged Web verified at ${new URL(targetURL).origin}\n`);
