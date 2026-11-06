// @govela/compiler: the compiler service (docs/TOOLING_PROTOCOL.md).
import { compile, startService, binaryPath, languageServerPath, version, protocol, type CompileResult, type Diagnostic, type Service } from '@govela/compiler';
import { expectTrue, expectType, type Equal } from './expect.ts';

expectType<string>(version);
expectTrue<Equal<typeof protocol, 1>>();
expectType<string>(binaryPath());
expectType<string>(languageServerPath());

const result = await compile({ path: 'src/App.vela', options: { css: 'external', target: 'browser', componentImports: 'vela' } });
expectTrue<Equal<typeof result, CompileResult>>();
for (const d of result.diagnostics) {
  expectType<Diagnostic>(d);
  expectType<'error' | 'warning' | 'hint'>(d.severity);
}
const first = result.diagnostics[0];
// @ts-expect-error noUncheckedIndexedAccess: the first diagnostic may be missing
first.message;
// @ts-expect-error an unknown CSS mode
compile({ path: 'A.vela', options: { css: 'inline' } });

const service: Service = await startService({ cwd: '.', args: ['--target', 'browser'] });
expectType<number>(service.handshake().protocol);
await service.compile({ path: 'A.vela', source: '<template><p></p></template>' });
await service.close();
