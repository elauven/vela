import postgres from 'postgres';

// Same pool size as the Go version (DB_POOL, default 20).
export const sql = postgres(process.env.DATABASE_URL || 'postgres://bench:bench@localhost:5432/bench', {
  max: Number(process.env.DB_POOL || 20),
});
