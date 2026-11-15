import process from "node:process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

const frontend = process.cwd();
const output = path.resolve(frontend, "../docs");
const summary = JSON.parse(
  await readFile("coverage/coverage-summary.json", "utf8"),
);
const portable = Object.fromEntries(
  Object.entries(summary).map(([key, value]) => [
    key === "total"
      ? key
      : `frontend/${path.relative(frontend, key).split(path.sep).join("/")}`,
    value,
  ]),
);
const lcov = (await readFile("coverage/lcov.info", "utf8")).replace(
  /^SF:(.*)$/gm,
  (_line, file) => {
    const relative = path.isAbsolute(file)
      ? path.relative(frontend, file)
      : file;
    return `SF:frontend/${relative.split(path.sep).join("/")}`;
  },
);
await mkdir(output, { recursive: true });
await writeFile(
  path.join(output, "frontend-coverage-summary.json"),
  JSON.stringify(portable, null, 2) + "\n",
);
await writeFile(path.join(output, "frontend-coverage.info"), lcov);
console.log(`Saved portable frontend coverage reports to ${output}`);
