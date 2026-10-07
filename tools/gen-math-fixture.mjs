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
const unaryNames = ['log', 'log2', 'log10', 'log1p', 'exp', 'expm1', 'sin', 'cos', 'tan', 'asin', 'acos', 'atan', 'sinh', 'cosh', 'tanh', 'asinh', 'acosh', 'atanh'];
const unary = [];
for (const name of unaryNames) {
  for (const x of specials) unary.push([name, bits(x), bits(Math[name](x))]);
  for (let i = 0; i < 1000; i++) {
    const kind = i % 8;
    const x = kind === 0 ? (rnd() * 2 - 1)
      : kind === 1 ? (rnd() * 40 - 20)
      : kind === 2 ? Math.exp(rnd() * 60 - 30)
      : kind === 3 ? (rnd() < 0.5 ? -1 : 1) * Math.exp(rnd() * 1380 - 690)
      : kind === 4 ? Math.round(rnd() * 2000 - 1000) * Math.PI / 2 + (rnd() - 0.5) * 1e-9
      : kind === 5 ? (rnd() < 0.5 ? -1 : 1) * Math.exp(rnd() * 40 - 745)
      : kind === 6 ? 1 + (rnd() - 0.5) * 1e-12
      : (rnd() * 1420 - 710);
    unary.push([name, bits(x), bits(Math[name](x))]);
  }
}
const atan2 = [];
for (let i = 0; i < 300; i++) { const y = rnd() * 20 - 10, x = rnd() * 20 - 10; atan2.push([bits(y), bits(x), bits(Math.atan2(y, x))]); }
const radix = [];
for (let i = 0; i < 1500; i++) {
  const r = 2 + Math.floor(rnd() * 35);
  const x = i % 4 === 0 ? Math.floor(rnd() * 1e6) : i % 4 === 1 ? (rnd() - 0.5) * 1000 : i % 4 === 2 ? Math.exp(rnd() * 80 - 40) : -Math.exp(rnd() * 100);
  if (r !== 10) radix.push([bits(x), r, x.toString(r)]);
}
process.stdout.write(JSON.stringify({
  node: process.version,
  unary,
  atan2,
  radix,
  pow: pow.map(([x, y]) => [bits(x), bits(y), bits(Math.pow(x, y))]),
  cbrt: cbrt.map((x) => [bits(x), bits(Math.cbrt(x))]),
}) + '\n');
