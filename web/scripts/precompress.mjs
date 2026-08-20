import { readdir, readFile, writeFile } from "node:fs/promises";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { brotliCompress, constants, gzip } from "node:zlib";
import { promisify } from "node:util";

const distDir = fileURLToPath(new URL("../dist/", import.meta.url));
const gzipAsync = promisify(gzip);
const brotliCompressAsync = promisify(brotliCompress);
// ponytail: build-time sidecars remove runtime compression CPU and avoid a compression dependency.
const compressibleExtensions = new Set([
  ".css",
  ".html",
  ".js",
  ".json",
  ".map",
  ".mjs",
  ".svg",
  ".txt",
  ".webmanifest",
  ".xml",
]);

async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const file = join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await walk(file));
    } else if (entry.isFile() && compressibleExtensions.has(extname(entry.name))) {
      files.push(file);
    }
  }
  return files;
}

let sourceBytes = 0;
let gzipBytes = 0;
let brotliBytes = 0;
let compressedFiles = 0;
const files = await walk(distDir);
for (const file of files) {
  const source = await readFile(file);
  if (source.byteLength < 1024) continue;

  const [gzipped, brotlied] = await Promise.all([
    gzipAsync(source, { level: 9 }),
    brotliCompressAsync(source, {
      params: {
        [constants.BROTLI_PARAM_QUALITY]: 11,
      },
    }),
  ]);
  await Promise.all([
    writeFile(`${file}.gz`, gzipped),
    writeFile(`${file}.br`, brotlied),
  ]);
  sourceBytes += source.byteLength;
  gzipBytes += gzipped.byteLength;
  brotliBytes += brotlied.byteLength;
  compressedFiles += 1;
}

console.log(`Precompressed ${compressedFiles} static files: source=${sourceBytes} gzip=${gzipBytes} br=${brotliBytes}`);
