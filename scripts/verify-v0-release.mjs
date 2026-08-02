import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  lstat,
  mkdtemp,
  mkdir,
  open,
  readFile,
  readdir,
  rm,
  stat,
} from "node:fs/promises";
import { createServer } from "node:net";
import { arch as hostArchitecture, platform as hostPlatform, tmpdir } from "node:os";
import { basename, join, posix, relative, resolve, sep } from "node:path";

import { verifyV0WebWithBundledChromium } from "./v0-browser-verify.mjs";

const rawArguments = process.argv.slice(2);
const staticOnly = rawArguments.includes("--static");
const positionalArguments = rawArguments.filter(
  (argument) => argument !== "--static",
);
const archivePath = positionalArguments[0]
  ? resolve(positionalArguments[0])
  : "";
const webSourceDirectory = positionalArguments[1]
  ? resolve(positionalArguments[1])
  : "";

if (
  positionalArguments.length < 1 ||
  positionalArguments.length > 2 ||
  !archivePath.endsWith(".tar.gz")
) {
  throw new Error(
    "usage: node scripts/verify-v0-release.mjs <release.tar.gz> [web-source-directory] [--static]",
  );
}
if (!staticOnly) assertWebSourceDirectory(webSourceDirectory);

const verificationRoot = await mkdtemp(
  join(tmpdir(), "go-workflow-v0-verify."),
);
let activeServer;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function assertWebSourceDirectory(directory) {
  assert(
    directory && basename(directory) !== "node_modules",
    "Web source directory is required for native browser verification",
  );
}

function parseJSON(source, label) {
  try {
    return JSON.parse(source);
  } catch (error) {
    throw new Error(`${label} is invalid JSON: ${error.message}`);
  }
}

function parseRuntimeUnits(source, label) {
  const value = parseJSON(source, label);
  assert(
    Array.isArray(value) &&
      value.length > 0 &&
      value.every((name) => typeof name === "string" && name.length > 0),
    `${label} must be a non-empty unit name array`,
  );
  assert(
    value.every((name, index) => index === 0 || value[index - 1] < name),
    `${label} must be strictly sorted without duplicates`,
  );
  return value;
}

function normalizeHostArchitecture(value) {
  if (value === "x64") return "amd64";
  if (value === "arm64") return "arm64";
  return value;
}

function validateRelease(release, packageName) {
  assert(
    release && typeof release === "object" && !Array.isArray(release),
    "RELEASE.json must contain an object",
  );
  assert(release.name === "go-workflow-v0", "release name is invalid");
  assert(
    typeof release.version === "string" &&
      /^[0-9]+\.[0-9]+\.[0-9]+(?:[.-][0-9A-Za-z.-]+)?$/.test(
        release.version,
      ),
    "release version is invalid",
  );
  assert(
    ["darwin", "windows", "linux"].includes(release.platform),
    `release platform is invalid: ${release.platform}`,
  );
  assert(
    ["amd64", "arm64"].includes(release.architecture),
    `release architecture is invalid: ${release.architecture}`,
  );
  assert(release.cgo_enabled === false, "desktop release must disable CGO");
  assert(
    ["native", "static"].includes(release.verification),
    "release verification mode is invalid",
  );
  const expectedPackageName = [
    release.name,
    release.version,
    release.platform,
    release.architecture,
  ].join("-");
  assert(
    packageName === expectedPackageName,
    `package directory is ${packageName}, expected ${expectedPackageName}`,
  );
  return release;
}

function validateArchiveEntries(source) {
  const entries = source.split(/\r?\n/).filter(Boolean);
  assert(entries.length > 0, "release archive is empty");
  const topLevelNames = new Set();
  const seen = new Set();
  for (const entry of entries) {
    assert(!entry.includes("\\"), `archive path uses a backslash: ${entry}`);
    assert(!posix.isAbsolute(entry), `archive path is absolute: ${entry}`);
    const components = entry.split("/").filter(Boolean);
    assert(
      components.length > 0 && !components.includes(".."),
      `archive path escapes its package directory: ${entry}`,
    );
    topLevelNames.add(components[0]);
    assert(!seen.has(entry), `archive contains a duplicate path: ${entry}`);
    seen.add(entry);
  }
  assert(
    topLevelNames.size === 1,
    `release archive must contain one package directory, found ${topLevelNames.size}`,
  );
  return [...topLevelNames][0];
}

async function listPackageFiles(directory, root = directory) {
  const files = [];
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const absolutePath = join(directory, entry.name);
    const details = await lstat(absolutePath);
    assert(!details.isSymbolicLink(), `package must not contain symlinks: ${absolutePath}`);
    if (details.isDirectory()) {
      files.push(...(await listPackageFiles(absolutePath, root)));
      continue;
    }
    assert(details.isFile(), `package contains a special file: ${absolutePath}`);
    files.push(relative(root, absolutePath).split(sep).join("/"));
  }
  return files.sort();
}

function parseChecksumLines(source, label) {
  const checksums = new Map();
  for (const line of source.trim().split(/\r?\n/)) {
    const match = line.match(/^([0-9a-f]{64}) [ *](.+)$/);
    assert(match, `${label} contains an invalid line: ${line}`);
    const fileName = match[2].startsWith("./") ? match[2].slice(2) : match[2];
    assert(
      fileName &&
        !fileName.includes("\\") &&
        !posix.isAbsolute(fileName) &&
        !fileName.split("/").includes(".."),
      `${label} contains an unsafe path: ${match[2]}`,
    );
    assert(!checksums.has(fileName), `${label} repeats ${fileName}`);
    checksums.set(fileName, match[1]);
  }
  return checksums;
}

async function sha256(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function verifyArchiveChecksum() {
  const checksumPath = `${archivePath}.sha256`;
  const checksums = parseChecksumLines(
    await readFile(checksumPath, "utf8"),
    basename(checksumPath),
  );
  assert(
    checksums.size === 1 && checksums.has(basename(archivePath)),
    `${basename(checksumPath)} must cover only ${basename(archivePath)}`,
  );
  assert(
    checksums.get(basename(archivePath)) === (await sha256(archivePath)),
    `archive checksum does not match ${basename(archivePath)}`,
  );
}

async function verifyPackageChecksums(packageDirectory) {
  const files = await listPackageFiles(packageDirectory);
  const coveredFiles = files.filter((fileName) => fileName !== "SHA256SUMS");
  const checksums = parseChecksumLines(
    await readFile(join(packageDirectory, "SHA256SUMS"), "utf8"),
    "SHA256SUMS",
  );
  assert(
    checksums.size === coveredFiles.length &&
      coveredFiles.every((fileName) => checksums.has(fileName)),
    "SHA256SUMS must cover every packaged file except itself exactly once",
  );
  for (const fileName of coveredFiles) {
    assert(
      checksums.get(fileName) ===
        (await sha256(join(packageDirectory, ...fileName.split("/")))),
      `SHA256SUMS does not match ${fileName}`,
    );
  }
}

async function verifyBinary(binaryPath, platform, architecture) {
  const handle = await open(binaryPath, "r");
  try {
    const header = Buffer.alloc(4096);
    const { bytesRead } = await handle.read(header, 0, header.length, 0);
    assert(bytesRead >= 64, `packaged binary is too small: ${binaryPath}`);

    let actualArchitecture;
    if (platform === "windows") {
      assert(header.subarray(0, 2).equals(Buffer.from("MZ")), "Windows binary is not PE");
      const peOffset = header.readUInt32LE(0x3c);
      assert(peOffset + 6 <= bytesRead, "Windows PE header is unavailable");
      assert(
        header.subarray(peOffset, peOffset + 4).equals(Buffer.from("PE\0\0")),
        "Windows PE signature is invalid",
      );
      const machine = header.readUInt16LE(peOffset + 4);
      actualArchitecture = { 0x8664: "amd64", 0xaa64: "arm64" }[machine];
    } else if (platform === "linux") {
      assert(
        header.subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])),
        "Linux binary is not ELF",
      );
      assert(header[4] === 2 && header[5] === 1, "Linux binary must be 64-bit little-endian ELF");
      actualArchitecture = { 0x3e: "amd64", 0xb7: "arm64" }[
        header.readUInt16LE(18)
      ];
    } else {
      assert(
        header.readUInt32LE(0) === 0xfeedfacf,
        "macOS binary is not 64-bit little-endian Mach-O",
      );
      actualArchitecture = {
        0x01000007: "amd64",
        0x0100000c: "arm64",
      }[header.readUInt32LE(4)];
    }
    assert(
      actualArchitecture === architecture,
      `binary architecture is ${actualArchitecture || "unknown"}, expected ${architecture}`,
    );
  } finally {
    await handle.close();
  }
}

async function verifyPackageShape(packageDirectory, release) {
  const binaryName =
    release.platform === "windows"
      ? "workflow-server.exe"
      : "workflow-server";
  const launcherName = release.platform === "windows" ? "run.cmd" : "run.sh";
  const binaryPath = join(packageDirectory, "bin", binaryName);
  const launcherPath = join(packageDirectory, launcherName);
  const binaryDetails = await stat(binaryPath);
  const launcherDetails = await stat(launcherPath);
  assert(binaryDetails.isFile(), `${binaryName} must be a regular file`);
  assert(launcherDetails.isFile(), `${launcherName} must be a regular file`);
  if (release.platform !== "windows") {
    assert(
      (binaryDetails.mode & 0o111) !== 0 && (launcherDetails.mode & 0o111) !== 0,
      "Unix binary and launcher must be executable",
    );
  } else {
    const launcherSource = await readFile(launcherPath, "utf8");
    assert(
      launcherSource.includes("bin\\workflow-server.exe") &&
        launcherSource.includes("%*"),
      "Windows launcher does not invoke the packaged binary with arguments",
    );
  }
  await verifyBinary(binaryPath, release.platform, release.architecture);

  const indexHTML = await readFile(join(packageDirectory, "web", "index.html"), "utf8");
  assert(indexHTML.includes('<div id="root"></div>'), "packaged Web entrypoint is invalid");
  const scriptPath = indexHTML.match(/<script[^>]+src="([^"]+\.js)"/)?.[1];
  assert(scriptPath, "packaged Web entrypoint does not reference an application bundle");
  const relativeScriptPath = scriptPath.replace(/^\/+/, "");
  assert(
    !relativeScriptPath.split("/").includes(".."),
    "packaged Web bundle path is unsafe",
  );
  assert(
    (await stat(join(packageDirectory, "web", ...relativeScriptPath.split("/")))).isFile(),
    "packaged Web application bundle is missing",
  );
  return { binaryPath, launcherPath };
}

function serverOutput(server) {
  return `${server.stdout}${server.stderr}`.trim();
}

async function availableAddress() {
  const probe = createServer();
  await new Promise((resolveListen, reject) => {
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", resolveListen);
  });
  const address = probe.address();
  assert(
    address && typeof address === "object",
    "unable to reserve a verification port",
  );
  await new Promise((resolveClose, reject) =>
    probe.close((error) => (error ? reject(error) : resolveClose())),
  );
  return `127.0.0.1:${address.port}`;
}

function startPackagedServer(
  packageDirectory,
  binaryPath,
  launcherPath,
  release,
  dataDirectory,
  address,
) {
  const homeDirectory = join(verificationRoot, "home");
  const env = {
    ...process.env,
    HOME: homeDirectory,
    USERPROFILE: homeDirectory,
    WORKFLOW_ADDR: address,
    WORKFLOW_DATA_DIR: dataDirectory,
    WORKFLOW_WEB_DIR: join(packageDirectory, "web"),
  };
  const executable =
    release.platform === "windows" ? binaryPath : launcherPath;
  const server = spawn(executable, {
    cwd: packageDirectory,
    env,
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const state = {
    process: server,
    platform: release.platform,
    stdout: "",
    stderr: "",
  };
  server.stdout.setEncoding("utf8");
  server.stderr.setEncoding("utf8");
  server.stdout.on("data", (chunk) => {
    state.stdout += chunk;
  });
  server.stderr.on("data", (chunk) => {
    state.stderr += chunk;
  });
  server.once("error", (error) => {
    state.stderr += `\n${error.stack || error.message}`;
  });
  return state;
}

async function stopPackagedServer(server) {
  if (!server || server.process.exitCode !== null) return;
  const stopped = new Promise((resolveExit, reject) => {
    const timeout = setTimeout(() => {
      server.process.kill("SIGKILL");
      reject(
        new Error(
          `packaged server did not stop cleanly:\n${serverOutput(server)}`,
        ),
      );
    }, 15_000);
    server.process.once("exit", (code, signal) => {
      clearTimeout(timeout);
      if (server.platform === "windows" || code === 0) resolveExit();
      else {
        reject(
          new Error(
            `packaged server exited with code=${code} signal=${signal}:\n${serverOutput(server)}`,
          ),
        );
      }
    });
  });
  server.process.kill(server.platform === "windows" ? "SIGTERM" : "SIGINT");
  await stopped;
}

async function fetchJSON(url, init) {
  const response = await fetch(url, init);
  const text = await response.text();
  assert(
    response.ok,
    `${init?.method || "GET"} ${url} returned ${response.status}: ${text}`,
  );
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new Error(`invalid JSON from ${url}: ${error.message}`);
  }
}

async function waitForWeb(baseURL, server) {
  const deadline = Date.now() + 8_000;
  let latestError;
  while (Date.now() < deadline) {
    if (server.process.exitCode !== null) {
      throw new Error(
        `packaged server exited during startup:\n${serverOutput(server)}`,
      );
    }
    try {
      const response = await fetch(`${baseURL}/`);
      const html = await response.text();
      assert(response.ok, `Web entrypoint returned ${response.status}`);
      assert(
        html.includes('<div id="root"></div>'),
        "Web entrypoint is not the V0 application",
      );
      const scriptPath = html.match(/<script[^>]+src="([^"]+\.js)"/)?.[1];
      assert(
        scriptPath,
        "Web entrypoint does not reference its application bundle",
      );
      const assetResponse = await fetch(new URL(scriptPath, baseURL));
      assert(
        assetResponse.ok,
        `Web application bundle returned ${assetResponse.status}`,
      );
      return;
    } catch (error) {
      latestError = error;
      await new Promise((resolveWait) => setTimeout(resolveWait, 40));
    }
  }
  throw new Error(
    `packaged server did not become ready: ${latestError?.message || "timeout"}\n${serverOutput(server)}`,
  );
}

async function waitForSuccessfulRun(baseURL, runID) {
  const deadline = Date.now() + 8_000;
  let latestRun;
  while (Date.now() < deadline) {
    try {
      const payload = await fetchJSON(
        `${baseURL}/v1/runs/${encodeURIComponent(runID)}`,
      );
      latestRun = payload.run;
      if (latestRun?.status === "success") return latestRun;
      if (latestRun?.status === "failed") {
        throw new Error(
          `verification run failed: ${JSON.stringify(latestRun)}`,
        );
      }
    } catch (error) {
      if (String(error.message).includes("verification run failed"))
        throw error;
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 40));
  }
  throw new Error(
    `verification run did not succeed: ${JSON.stringify(latestRun)}`,
  );
}

async function assertExactMode(path, expectedMode) {
  const details = await stat(path);
  const actualMode = details.mode & 0o777;
  assert(
    actualMode === expectedMode,
    `${path} mode is ${actualMode.toString(8)}, expected ${expectedMode.toString(8)}`,
  );
}

async function assertWindowsDataShape(dataDirectory) {
  for (const path of [
    dataDirectory,
    join(dataDirectory, "workflow.db"),
    join(dataDirectory, "credentials"),
  ]) {
    await stat(path);
  }
}

async function verifyPackagedWeb(baseURL) {
  await verifyV0WebWithBundledChromium(`${baseURL}/`, webSourceDirectory);
}

try {
  await verifyArchiveChecksum();
  const listing = spawnSync("tar", ["-tzf", archivePath], {
    encoding: "utf8",
  });
  assert(
    listing.status === 0,
    `unable to inspect ${basename(archivePath)}: ${listing.stderr}`,
  );
  const archivePackageName = validateArchiveEntries(listing.stdout);

  const extractDirectory = join(verificationRoot, "archive");
  const dataDirectory = join(verificationRoot, "data");
  await mkdir(extractDirectory, { recursive: true });
  await mkdir(join(verificationRoot, "home"), { recursive: true });
  const extraction = spawnSync(
    "tar",
    ["-xzf", archivePath, "-C", extractDirectory],
    { encoding: "utf8" },
  );
  assert(
    extraction.status === 0,
    `unable to extract ${basename(archivePath)}: ${extraction.stderr}`,
  );
  const packageEntries = await readdir(extractDirectory, {
    withFileTypes: true,
  });
  assert(
    packageEntries.length === 1 && packageEntries[0].isDirectory(),
    "release archive must extract to one package directory",
  );
  assert(
    packageEntries[0].name === archivePackageName,
    "archive listing and extracted package directory differ",
  );
  const packageDirectory = join(extractDirectory, archivePackageName);
  const release = validateRelease(
    parseJSON(
      await readFile(join(packageDirectory, "RELEASE.json"), "utf8"),
      "RELEASE.json",
    ),
    archivePackageName,
  );
  await verifyPackageChecksums(packageDirectory);
  const { binaryPath, launcherPath } = await verifyPackageShape(
    packageDirectory,
    release,
  );
  const manifestRuntimeUnits = parseRuntimeUnits(
    await readFile(join(packageDirectory, "RUNTIME_UNITS.json"), "utf8"),
    "packaged runtime unit manifest",
  );

  if (staticOnly) {
    process.stdout.write(
      `V0 static release verification passed: ${basename(archivePath)}, ${release.platform}/${release.architecture}, ${manifestRuntimeUnits.length} declared runtime units, binary format + archive integrity\n`,
    );
  } else {
    const currentPlatform = hostPlatform();
    const currentArchitecture = normalizeHostArchitecture(hostArchitecture());
    assert(
      release.platform === currentPlatform &&
        release.architecture === currentArchitecture,
      `native verification requires ${release.platform}/${release.architecture}, current host is ${currentPlatform}/${currentArchitecture}; use --static only for cross-built artifacts`,
    );

    if (release.platform === "windows") {
      const launcherCheck = spawnSync(launcherPath, ["--version"], {
        cwd: packageDirectory,
        encoding: "utf8",
        env: {
          ...process.env,
          WORKFLOW_DATA_DIR: dataDirectory,
        },
        shell: true,
        windowsHide: true,
      });
      assert(
        launcherCheck.status === 0 &&
          launcherCheck.stdout.startsWith(
            `workflow-server ${release.version} (`,
          ),
        `Windows launcher failed: ${launcherCheck.stderr}`,
      );
    }

    const runtimeUnitCommand = spawnSync(
      binaryPath,
      ["--runtime-units"],
      { encoding: "utf8" },
    );
    assert(
      runtimeUnitCommand.status === 0,
      `packaged runtime unit command failed: ${runtimeUnitCommand.stderr}`,
    );
    const binaryRuntimeUnits = parseRuntimeUnits(
      runtimeUnitCommand.stdout,
      "packaged binary runtime units",
    );
    assert(
      JSON.stringify(manifestRuntimeUnits) ===
        JSON.stringify(binaryRuntimeUnits),
      "packaged runtime unit manifest does not match the binary",
    );

    const address = await availableAddress();
    const baseURL = `http://${address}`;
    activeServer = startPackagedServer(
      packageDirectory,
      binaryPath,
      launcherPath,
      release,
      dataDirectory,
      address,
    );
    await waitForWeb(baseURL, activeServer);
    await verifyPackagedWeb(baseURL);

    const runID = "v0-package-verification-run";
    const accepted = await fetchJSON(`${baseURL}/v1/runs?wait=false`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        definition: {
          id: "v0-package-verification",
          name: "V0 package verification",
          entry_nodes: ["verify"],
          nodes: [
            {
              id: "verify",
              name: "Verify packaged runtime",
              executor: { type: "unit", ref: "LogUnit" },
              input: "packaged runtime is operational",
            },
          ],
        },
        run: { id: runID },
      }),
    });
    assert(
      accepted.run?.id === runID,
      "run submission did not preserve its client ID",
    );
    const completed = await waitForSuccessfulRun(baseURL, runID);
    assert(
      completed.node_runs?.verify?.status === "success",
      "packaged LogUnit did not complete successfully",
    );
    const firstEvents = await fetchJSON(
      `${baseURL}/v1/runs/${runID}/events`,
    );
    assert(
      firstEvents.events.filter((event) => event.type === "run_started")
        .length === 1,
      "verification run must emit exactly one run_started event",
    );

    if (release.platform === "windows") {
      await assertWindowsDataShape(dataDirectory);
    } else {
      await assertExactMode(dataDirectory, 0o700);
      await assertExactMode(join(dataDirectory, "workflow.db"), 0o600);
      await assertExactMode(join(dataDirectory, "credentials"), 0o700);
    }
    await stopPackagedServer(activeServer);
    activeServer = undefined;

    activeServer = startPackagedServer(
      packageDirectory,
      binaryPath,
      launcherPath,
      release,
      dataDirectory,
      address,
    );
    await waitForWeb(baseURL, activeServer);
    const restored = await fetchJSON(`${baseURL}/v1/runs/${runID}`);
    assert(
      restored.run?.status === "success",
      "successful run was not restored after restart",
    );
    const restoredEvents = await fetchJSON(
      `${baseURL}/v1/runs/${runID}/events`,
    );
    assert(
      restoredEvents.events.filter((event) => event.type === "run_started")
        .length === 1,
      "restart changed the persisted run_started event count",
    );
    await stopPackagedServer(activeServer);
    activeServer = undefined;

    process.stdout.write(
      `V0 native release verification passed: ${basename(archivePath)}, ${binaryRuntimeUnits.length} runtime units, browser creation + execution + restore, API runtime + restart persistence\n`,
    );
  }
} finally {
  if (activeServer) {
    try {
      await stopPackagedServer(activeServer);
    } catch (error) {
      process.stderr.write(`${error.message}\n`);
    }
  }
  await rm(verificationRoot, { recursive: true, force: true });
}
