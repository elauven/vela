# SvelteKit version of the benchmark tracker. Build from bench/:
#   docker build -f sveltekit-app/Dockerfile -t bench-sveltekit .
FROM node:24 AS build
ARG ORIGIN=http://localhost:8082
WORKDIR /app/sveltekit-app
COPY shared /app/shared
COPY sveltekit-app/package.json sveltekit-app/package-lock.json ./
RUN npm ci
COPY sveltekit-app ./
RUN ORIGIN=$ORIGIN npm run build && npm prune --omit=dev

FROM node:24-slim
COPY --from=build /app /app
WORKDIR /app/sveltekit-app
ENV NODE_ENV=production PORT=8082 BODY_SIZE_LIMIT=100M STORAGE_DIR=/app/storage WORKERS=1
EXPOSE 8082
# One process, or WORKERS processes behind node:cluster.
CMD ["sh", "-c", "if [ \"$WORKERS\" = 1 ]; then exec node build/index.js; else exec node cluster.js; fi"]
