import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

export async function verifyV0WebWithBundledChromium(
  targetURL,
  webSourceDirectory,
) {
  const parsedTarget = new URL(targetURL);
  assert(parsedTarget.protocol === "http:", "V0 browser verification requires HTTP");
  assert(
    ["127.0.0.1", "localhost", "[::1]"].includes(parsedTarget.hostname),
    "V0 browser verification only accepts a loopback target",
  );
  assert(
    !parsedTarget.username && !parsedTarget.password,
    "V0 browser verification target cannot contain credentials",
  );

  const webRoot = resolve(webSourceDirectory);
  const playwrightURL = pathToFileURL(
    join(webRoot, "node_modules", "@playwright", "test", "index.mjs"),
  ).href;
  const smokeURL = pathToFileURL(
    join(webRoot, "tests", "e2e", "packaged-web-smoke.mjs"),
  ).href;
  const [{ chromium }, { runPackagedWebSmoke }] = await Promise.all([
    import(playwrightURL),
    import(smokeURL),
  ]);
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ locale: "zh-CN" });
    const page = await context.newPage();
    await runPackagedWebSmoke(page, parsedTarget.href);
    await context.close();
  } finally {
    await browser.close();
  }
}
