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

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

# 127.0.0.1 rather than localhost: nginx listens on IPv4 only, and inside the
# container "localhost" resolves to ::1 first, which wget will not fall back from.
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
    CMD wget --quiet --tries=1 -O /dev/null http://127.0.0.1/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
