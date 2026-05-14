const m = await import('@electron/asar');
const [,, action, src, dest] = process.argv;
try {
  if (action === 'extract') await m.extractAll(src, dest);
  else if (action === 'pack') await m.createPackageWithOptions(src, dest, {});
  process.exit(0);
} catch (e) {
  console.error(e.message);
  process.exit(1);
}
