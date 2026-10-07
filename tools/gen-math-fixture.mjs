// Generates testdata/math_fixture.json: Math.pow and Math.cbrt results of
// this Node version as IEEE-754 bit patterns, for TestFdlibmMatchesNode.
// Run: node tools/gen-math-fixture.mjs > testdata/math_fixture.json
const view = new DataView(new ArrayBuffer(8));
const bits = (x) => { view.setFloat64(0, x); return view.getBigUint64(0).toString(16).padStart(16, '0'); };
let seed = 20261007;
const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
const specials = [0, -0, 1, -1, 0.5, -0.5, 2, -2, 3, 10, 1e-310, -1e-310, 5e-324, 1e308, -1e308, Infinity, -Infinity, NaN, 1 + 2 ** -52, 1 - 2 ** -53, 0.1, 7, 1e21, 2 ** 31, 2 ** 53, 2 ** 64];
const pow = [];
for (const x of specials) for (const y of specials) pow.push([x, y]);
for (let i = 0; i < 2500; i++) {
  const kind = i % 5;
  const x = kind === 0 ? rnd() * 200 - 20 : kind === 1 ? rnd() * 2 : kind === 2 ? Math.exp(rnd() * 1400 - 700) : kind === 3 ? -(rnd() * 10) : 1 + (rnd() - 0.5) * 1e-6;
  const y = kind === 3 ? Math.floor(rnd() * 40 - 20) : kind === 4 ? (rnd() - 0.5) * 2e9 : rnd() * 16 - 8;
  pow.push([x, y]);
}
const cbrt = specials.slice();
for (let i = 0; i < 2500; i++) cbrt.push((i % 3 === 0 ? -1 : 1) * Math.exp(rnd() * 1450 - 745));
process.stdout.write(JSON.stringify({
  node: process.version,
  pow: pow.map(([x, y]) => [bits(x), bits(y), bits(Math.pow(x, y))]),
  cbrt: cbrt.map((x) => [bits(x), bits(Math.cbrt(x))]),
}) + '\n');
