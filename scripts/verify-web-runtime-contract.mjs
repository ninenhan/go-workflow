import { spawnSync } from "node:child_process";
import { access, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const backendRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const webRoot = resolve(process.argv[2] || "");

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

await access(join(webRoot, "package.json"));
await access(join(webRoot, "scripts", "vite-audit-server.mjs"));

const browserVerifierSource = await readFile(
  join(backendRoot, "scripts", "v0-browser-verify.mjs"),
  "utf8",
);
assert(
  /chromium\.launch\(\{\s*headless:\s*true\s*\}\)/.test(browserVerifierSource),
  "release verification must launch Playwright's bundled Chromium",
);
for (const forbiddenBrowserOverride of [
  /\bchannel\s*:/,
  /\bexecutablePath\s*:/,
  /Google Chrome/i,
  /Applications\/.*Chrome/i,
  /Program Files.*Chrome/i,
]) {
  assert(
    !forbiddenBrowserOverride.test(browserVerifierSource),
    "release verification must not launch a system browser",
  );
}
const releaseVerifierSource = await readFile(
  join(backendRoot, "scripts", "verify-v0-release.mjs"),
  "utf8",
);
assert(
  releaseVerifierSource.includes("verifyV0WebWithBundledChromium"),
  "release verification must use the shared browser verifier",
);

const runtimeCommand = spawnSync(
  "go",
  ["run", "./cmd/workflow-server", "--runtime-units"],
  {
    cwd: backendRoot,
    encoding: "utf8",
  },
);
assert(
  runtimeCommand.status === 0,
  `unable to inspect backend runtime units: ${runtimeCommand.stderr}`,
);

let runtimeUnitNames;
try {
  runtimeUnitNames = JSON.parse(runtimeCommand.stdout);
} catch (error) {
  throw new Error(`backend runtime units are invalid JSON: ${error.message}`);
}
assert(
  Array.isArray(runtimeUnitNames) &&
    runtimeUnitNames.length > 0 &&
    runtimeUnitNames.every(
      (name) => typeof name === "string" && name.trim() === name && name,
    ),
  "backend runtime units must be a non-empty name array",
);
assert(
  runtimeUnitNames.every(
    (name, index) => index === 0 || runtimeUnitNames[index - 1] < name,
  ),
  "backend runtime units must be strictly sorted without duplicates",
);

const previousDirectory = process.cwd();
process.chdir(webRoot);
const auditModuleURL = pathToFileURL(
  join(webRoot, "scripts", "vite-audit-server.mjs"),
).href;
const { createAuditServer } = await import(auditModuleURL);
const server = await createAuditServer();

try {
  const { AVAILABLE_UNIT_CATALOG } = await server.ssrLoadModule(
    "/src/units/catalog.ts",
  );
  const directRuntimeActions = AVAILABLE_UNIT_CATALOG.filter(
    (unit) => unit.runtimeSupported === true,
  )
    .map((unit) => unit.id)
    .sort();
  const runtimeUnitSet = new Set(runtimeUnitNames);
  const missing = directRuntimeActions.filter(
    (unitID) => !runtimeUnitSet.has(unitID),
  );
  assert(
    missing.length === 0,
    `Web actions are missing from the backend runtime: ${missing.join(", ")}`,
  );

  const compositeActions = AVAILABLE_UNIT_CATALOG.filter(
    (unit) => unit.runtimeSupported === false,
  ).map((unit) => unit.id);
  assert(
    compositeActions.length === 1 &&
      compositeActions[0] === "RunShortcutUnit",
    `unexpected frontend-materialized actions: ${compositeActions.join(", ")}`,
  );

  process.stdout.write(
    `Web/runtime contract verified: ${directRuntimeActions.length} direct actions, ${compositeActions.length} frontend-materialized action, ${runtimeUnitNames.length} registered runtime units\n`,
  );
} finally {
  await server.close();
  process.chdir(previousDirectory);
}
