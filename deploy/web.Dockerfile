FROM nginxinc/nginx-unprivileged:1.27-alpine

COPY web/ /usr/share/nginx/html/
