# GENYEE homepage source

The Next.js homepage is one part of the unified site. Run `npm run setup`, `npm run build`, and `npm run preview` from the parent directory to include both the homepage and the complete `/docs/` site. Deployment instructions are in the parent README.

- `app/page.tsx`: product content and same-origin documentation links.
- `app/home.module.css`: homepage layout.
- `app/interactive.tsx`: responsive navigation and theme switch.
- `app/adopters.ts`: the six enterprise industry scenarios and deployment patterns showcased on the homepage. `variant: "A"` centers a horizontal logo alone; `variant: "B"` places a square icon beside the scenario name. Icons live in `public/docs/_home/adopters/`. Asset details are recorded in `BRAND-ASSETS.md`.
- `app/adopter-showcase.tsx`, `app/adopters.module.css`: a compact scenario strip below the hero. Two equal-width copies form a continuous linear CSS loop; hovering the items pauses the strip and reveals a floating detail panel, while touch users tap an item.
- `app/theme.ts`: VitePress-compatible theme persistence and pre-paint initialization.
- `../shared/brand.css`: shared light/dark design tokens.
- `public/docs/_home/`: homepage images. The site serves only `/` and `/docs/`, so build output and public assets both live under `/docs/_home/` (`homeAssets` in `../shared/header.ts`).

`npm run dev` can still preview the homepage alone during development. `/docs/` requires the combined preview from the parent directory.
