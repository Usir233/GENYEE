# Subpath Deployment

GENYEE can be built for deployment under a URL prefix such as `https://example.com/genyee/`.

This setup assumes the frontend and API share a public origin and an external reverse proxy strips the prefix before forwarding requests to GENYEE's internal Nginx server.

## Configuration

Set the frontend base path at build time:

```bash
docker build \
  --build-arg VITE_BASE_PATH=/genyee/ \
  -t genyee/genyee-ui:subpath \
  frontend/
```

For a local frontend build:

```bash
cd frontend
VITE_BASE_PATH=/genyee/ npm run build
```

Configure the backend's externally visible frontend URL:

```dotenv
FRONTEND_BASE_URL=https://example.com/genyee
```

The external OIDC callback URL should be registered with the identity provider as:

```text
https://example.com/genyee/api/v1/auth/oidc/callback
```

## Reverse Proxy

The external gateway should remove `/genyee/` before forwarding requests to the internal frontend Nginx server.

Example Nginx configuration, inside the `http` context:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' '';
}

server {
    # Configure your HTTPS listener and certificates.

    location = /genyee {
        return 308 /genyee/;
    }

    location ^~ /genyee/ {
        proxy_pass http://genyee-frontend:80/;

        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

Replace `genyee-frontend:80` with the internal frontend service address.

The trailing slash in `proxy_pass` is important. It ensures `/genyee/api/v1/...` is forwarded internally as `/api/v1/...`.

The internal frontend Nginx configuration continues to serve files and proxy APIs at their original root-relative paths.

## Compatibility and Limitations

- Root-path deployments remain the default when `VITE_BASE_PATH` is unset.
- Changing `VITE_BASE_PATH` requires rebuilding the frontend assets or image.
- This configuration does not provide runtime-switchable base paths.
- Auxiliary integrations and complete embedded-page workflows require separate validation.
- Full OIDC login with a live identity provider should be verified before production rollout.