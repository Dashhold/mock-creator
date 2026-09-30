# Build stage
FROM node:20-alpine AS builder

WORKDIR /app

# Copy the lockfile too and use `npm ci`, so the image gets exactly the
# dependency tree that was tested rather than a fresh resolution.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

# Runtime stage
FROM nginx:1.27-alpine

# openssl hashes the optional login password when the container starts. The
# stock default site is removed so only this app's configuration is served.
RUN apk add --no-cache openssl \
    && rm -f /etc/nginx/conf.d/default.conf

COPY --from=builder /app/dist /usr/share/nginx/html

# nginx.conf is a template: the image's entrypoint renders it to
# /etc/nginx/conf.d/default.conf when the container starts, and the script then
# writes the environment-dependent files it includes. Line endings are
# normalised because a Windows checkout can add CRs, which sh cannot run.
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY docker-entrypoint.d/40-mock-creator.sh /docker-entrypoint.d/40-mock-creator.sh
RUN sed -i 's/\r$//' /etc/nginx/templates/default.conf.template /docker-entrypoint.d/40-mock-creator.sh \
    && chmod 755 /docker-entrypoint.d/40-mock-creator.sh

# Defaults for docker compose. Railway provides PORT itself; set API_UPSTREAM to
# the API's private address there.
ENV PORT=80 \
    API_UPSTREAM=api:8080

EXPOSE 80

# 127.0.0.1 rather than localhost: inside the container "localhost" resolves to
# ::1 first, which wget will not fall back from. /healthz stays open when a login
# is required, so the check is never refused.
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
    CMD wget --quiet --tries=1 -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1

CMD ["nginx", "-g", "daemon off;"]
