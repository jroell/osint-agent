FROM oven/bun:1.2-alpine AS base
WORKDIR /app

FROM base AS deps
COPY package.json bun.lock* ./
COPY packages/shared-types/package.json packages/shared-types/package.json
COPY apps/api/package.json apps/api/package.json
RUN bun install --frozen-lockfile

FROM base AS build
COPY --from=deps /app/node_modules node_modules
COPY packages/shared-types packages/shared-types
COPY apps/api apps/api
COPY tsconfig.base.json .
WORKDIR /app/apps/api

FROM base AS runtime
COPY --from=build /app /app
WORKDIR /app/apps/api
ENV NODE_ENV=production
EXPOSE 3000
CMD ["bun", "run", "src/index.ts"]
