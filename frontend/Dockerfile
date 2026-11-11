# dev: Vite dev server with HMR; source is bind-mounted by docker-compose.dev.yml.
FROM node:24-alpine AS dev
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
EXPOSE 5173
CMD ["npm", "run", "dev"]

FROM dev AS build
RUN npm run build-only

# Default target: static build served by nginx, /api proxied to the backend.
FROM nginx:stable-alpine-slim
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
