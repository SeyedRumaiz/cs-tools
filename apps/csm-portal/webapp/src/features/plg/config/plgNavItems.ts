/**
 * PLG's entry in csm-portal's navigation.
 *
 * WHAT THE MERGE CHANGED. Standalone, PLG had its own sidebar with three
 * groups — Overview, Workspace, Manage — because it was a whole application.
 * Inside csm-portal it is one section beside Support and Operations, and those
 * three groups become its five rail entries.
 *
 * EVERY CHILD CARRIES A `tab`, AND IT HAS TO. `CsmSideBar.isSubmenuSection`
 * renders a section's children as rail entries only when *every* child has one
 * — it is the structural marker that distinguishes a rail submenu (Operations,
 * Security Center) from a section whose children live in an in-page tab strip
 * (Customers, Settings). Without it PLG rendered as a single flat item and its
 * other four pages were reachable only through search.
 *
 * PLG's children are route-backed rather than query-param tabs, which is the
 * less common of the two shapes this field supports. `navNodeHref` handles it:
 * given both a `tab` and a `routes` entry it navigates to `routes[0]`, so these
 * land on real paths rather than `?tab=` queries.
 *
 * Declared here rather than inline in `csmNavItems.ts` so the merge adds a file
 * and changes one line, and so a later change to PLG's pages does not touch
 * csm-portal's navigation source.
 *
 * The `id`s are a public contract: deployments key
 * `CSM_PORTAL_FEATURE_OVERRIDES` off them to hide or disable a page, so
 * renaming one is a breaking config change. `plg` hides the whole section —
 * which is what a deployment without the PLG backend should do.
 */
import {
  Building2,
  ChartColumn,
  Gauge,
  LayoutDashboard,
  Rocket,
  Sparkles,
  SquareCheckBig,
} from "@wso2/oxygen-ui-icons-react";

import type { CsmNavSection } from "@config/csmNavItems";

/** Base path for every PLG page. Matches the `/plg` API prefix, deliberately. */
export const PLG_BASE = "/plg";

export const PLG_NAV_SECTION: CsmNavSection = {
  id: "plg",
  label: "PLG",
  href: `${PLG_BASE}/dashboard`,
  icon: Rocket,
  // CS engineer and admin only, mirroring the backend's PermUsePlg. Set on the
  // section so every page inherits it — `requirements()` walks down from here,
  // so a page added later is covered without being listed.
  //
  // Narrower than canWrite/canUseOperations elsewhere in the portal by intent:
  // a view-only role holds PermView and could open PLG's pages, but every PLG
  // route would answer 403. A section that cannot be used is worse offered than
  // withheld. Manage Playbooks is the one page with a second, narrower rule —
  // it stays visible here and renders read-only; see PlaybooksPage.
  requires: "canUsePlg",
  children: [
    {
      // Labelled Overview, but this is the leadership view — see
      // LeadershipDashboardPage. The label is what leadership calls the page;
      // the component name is what it shows.
      id: "plg.overview",
      tab: "overview",
      label: "Overview",
      href: `${PLG_BASE}/overview`,
      routes: [`${PLG_BASE}/overview`],
      icon: Gauge,
    },
    {
      // Standalone this was Workspace > Dashboard, and PLG's landing page. It
      // carries two tiles the leadership view above does not — the queue
      // counts, which belong to whoever works the queue. The two were one
      // component until the variant flag proved too fragile; they now share
      // only their charts.
      id: "plg.dashboard",
      tab: "dashboard",
      label: "Dashboard",
      href: `${PLG_BASE}/dashboard`,
      routes: [`${PLG_BASE}/dashboard`],
      icon: ChartColumn,
    },
    {
      id: "plg.work-queue",
      tab: "work_queue",
      label: "My Work",
      href: `${PLG_BASE}/work-queue`,
      routes: [`${PLG_BASE}/work-queue`],
      icon: SquareCheckBig,
    },
    {
      id: "plg.organizations",
      tab: "organizations",
      label: "Organisations",
      href: `${PLG_BASE}/organizations`,
      // The detail route is listed so the sidebar keeps this tab selected while
      // an engineer is inside one organisation's pairing view.
      routes: [`${PLG_BASE}/organizations`],
      icon: Building2,
    },
    {
      id: "plg.new-registrations",
      tab: "new_registrations",
      label: "New Registrations",
      href: `${PLG_BASE}/new-registrations`,
      routes: [`${PLG_BASE}/new-registrations`],
      icon: Sparkles,
    },
    {
      id: "plg.playbooks",
      tab: "playbooks",
      label: "Manage Playbooks",
      href: `${PLG_BASE}/playbooks`,
      routes: [`${PLG_BASE}/playbooks`],
      icon: LayoutDashboard,
    },
  ],
};
