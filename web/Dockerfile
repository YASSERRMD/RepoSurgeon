# syntax=docker/dockerfile:1

# Web image: build the Vue 3 app, then serve the static bundle with nginx.
# The API upstream is injected at container start so the same image works in
# development and production without rebuilding.
FROM node:24-alpine AS build

WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ .
ARG VITE_APP_VERSION=0.1.0
ENV VITE_APP_VERSION=${VITE_APP_VERSION}
RUN npm run build

FROM nginx:1.27-alpine AS runtime

COPY web/docker/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html

# API_UPSTREAM points at the in-cluster API service, e.g. http://api:8080.
ENV API_UPSTREAM=http://api:8080
EXPOSE 80

CMD ["/bin/sh", "-c", "envsubst '${API_UPSTREAM}' < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf && exec nginx -g 'daemon off;'"]
