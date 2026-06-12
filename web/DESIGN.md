# Phantom Lancer Web Design System

Phantom Lancer uses a Quiet Agent Workbench / Quiet DevOps Control Plane style. The interface is a personal server console for technical owner workflows, not a marketing site, portfolio, or decorative dashboard.

## Design Dials

- Design variance: 4/10. Prefer familiar control-plane patterns over expressive layouts.
- Motion intensity: 2/10. Motion is only for state continuity; always respect reduced motion.
- Visual density: 7/10. Desktop-first workbench density is preferred over mobile-first spaciousness.

## Core Rules

- Use light neutral surfaces, thin borders, small radii, low-contrast hover states, and one restrained accent.
- Keep status colors semantic: green for available/success, orange for warning/stale/offline, red for destructive or failed states.
- Use primary navigation only for independent product domains. Use secondary tabs, drawers, inspectors, or collapsible details inside each domain.
- Keep full configuration forms inside their feature domain or Settings sub-tab. Dashboard may summarize state but must not duplicate full setup flows.
- Preserve a stable workbench structure: global nav, main work area, context list, and right inspector where helpful.
- Avoid hero sections, gradients, glass effects, marketing CTA composition, decorative illustrations, bokeh/orbs, and AI-purple visual language.
- Use monospace for technical values such as paths, commands, tokens, ids, ports, versions, endpoints, and logs.
- Modal and drawer surfaces must define dialog semantics, initial focus, Escape close, focus containment, and overscroll containment.
- Tabs and filters that represent meaningful product state should be reflected in the URL.
- Destructive actions must use the shared danger confirmation flow, including object name, impact, and recovery note.

## Component Defaults

- Panels: `rounded-lg`, `border var(--line)`, white surface, no nested card stacks.
- Secondary tabs: quiet tablist with selected surface background and a 2px accent underline.
- Buttons: text labels are acceptable for clear commands; use compact sizing for secondary action clusters.
- Icons: optional in the current text-first console. If introduced, use one lightweight linear icon library across the app.
- Forms: every direct field control needs a meaningful `name`; credentials and paths should disable spellcheck and browser autocomplete where appropriate.
- Images: provide intrinsic width and height whenever an image URL is rendered.
