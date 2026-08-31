FROM library/node:20.19-alpine AS builder

USER root
RUN mkdir -p /usr/src/app
WORKDIR /usr/src/app
COPY ./package.json /usr/src/app
COPY ./package-lock.json /usr/src/app
COPY ./.npmrc /usr/src/app
RUN npm ci
COPY . /usr/src/app
RUN ls -al
RUN npm run build && ls -al dist/spa

FROM library/nginx:alpine
COPY --from=builder /usr/src/app/dist/spa /usr/share/nginx/html
COPY nginx.conf /etc/nginx/

EXPOSE 80
EXPOSE 443
