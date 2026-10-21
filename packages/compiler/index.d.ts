// Types of the compiler service protocol (docs/TOOLING_PROTOCOL.md).

export declare const version: string;
export declare const protocol: 1;

/** The file options of a request; each is the build flag of the same name. */
export interface CompileOptions {
  minify?: boolean;
  css?: 'inject' | 'external';
  sourcemap?: boolean;
  target?: 'permissive' | 'browser' | 'worker' | 'node' | 'ssr';
  unavailableCapabilities?: string[];
  strict?: boolean;
  hydrationVerify?: 'repair' | 'fail' | 'off';
  mode?: 'development' | 'production';
  runtimeImport?: 'relative' | 'bare';
  checkDeps?: boolean;
  /** 'vela' keeps `.vela` specifiers of component imports, for bundler plugins. */
  componentImports?: 'js' | 'vela';
}

export interface CompileRequest {
  /** The component, absolute or relative to the service's working directory. */
  path: string;
  /** The text to compile; without it the service reads path. */
  source?: string;
  options?: CompileOptions;
}

export interface Diagnostic {
  severity: 'error' | 'warning' | 'hint';
  code?: string;
  file?: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
  message: string;
  suggestion?: string;
}

export interface ModuleImport {
  specifier: string;
  kind: 'component' | 'module' | 'runtime' | 'package' | 'url';
  resolved?: string;
}

export interface CompileResult {
  ok: boolean;
  js?: string;
  css?: string;
  /** A v3 source map with options.sourcemap. */
  map?: object;
  diagnostics: Diagnostic[];
  imports: ModuleImport[];
  error?: string;
}

export interface Handshake {
  protocol: number;
  compilerVersion: string;
  runtimePackage: string;
  runtimeContract: string;
}

export interface ServiceOptions {
  /** Working directory of the service: paths and vela.config.json resolve against it. */
  cwd?: string;
  /** A configuration file instead of vela.config.json in cwd. */
  config?: string;
  /** Further flags of `vela serve`, e.g. ['--target', 'browser']. */
  args?: string[];
  /** A vela binary instead of the platform package's. */
  binary?: string;
}

export interface Service {
  handshake(): Handshake;
  compile(request: CompileRequest): Promise<CompileResult>;
  /** Ends the input; resolves when the service has answered every request and exited. */
  close(): Promise<void>;
}

/** The vela binary: VELA_BINARY, else the binary of the platform package. */
export declare function binaryPath(): string;

/** Starts `vela serve --stdio`; rejects a binary of another protocol or version. */
export declare function startService(options?: ServiceOptions): Promise<Service>;

/** Compiles one component with a short-lived service. */
export declare function compile(request: CompileRequest, options?: ServiceOptions): Promise<CompileResult>;
