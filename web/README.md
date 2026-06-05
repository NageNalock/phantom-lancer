# Phantom Lancer Web

The frontend source lives in `web/src` and builds into `web/dist`, which is embedded by the Go server.

```bash
cd web
npm install
npm run dev
npm run build
```

Do not edit `web/dist` by hand. Keep runtime API access behind the Go backend under `/api/*`; the browser must not connect directly to shell processes, Codex app-server, or local system services.

The app is a typed React workbench. Page entry files should compose feature components and keep API calls, domain labels, rich-text parsing, and reusable UI primitives in their own modules under `src`.
