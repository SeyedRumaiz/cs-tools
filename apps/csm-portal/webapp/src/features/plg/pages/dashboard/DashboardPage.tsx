import { Box, Grid } from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { useNavigate } from "react-router";

import { useDashboard } from "@features/plg/api/hooks";
import { DashboardCharts } from "@features/plg/components/DashboardCharts";
import { ErrorBlock, LoadingBlock, PageHeader, StatTile } from "@features/plg/components/common";
import { PeriodSelect } from "@features/plg/components/PeriodSelect";
import { DEFAULT_RANGE_DAYS, isoDaysAgo } from "@features/plg/components/period";

/**
 * The workspace dashboard: what the engineer working the queue should do next.
 *
 * It leads with the two queue counts, and every tile is a link into the work.
 * The leadership view is a separate page — see LeadershipDashboardPage, which
 * shows the same standing picture without the to-do list.
 *
 * The period control scopes the cohort charts only. The queue counts ignore it
 * on purpose: a backlog that shrinks because someone changed a dropdown is a
 * backlog nobody clears.
 */
export default function DashboardPage(): JSX.Element {
  const navigate = useNavigate();
  const [days, setDays] = useState(DEFAULT_RANGE_DAYS);
  const { data, isPending, error } = useDashboard(isoDaysAgo(days));

  if (error) return <ErrorBlock error={error} />;

  return (
    // One root element, because csm-portal's AppLayout renders <Outlet /> into
    // a column flex box: a fragment would make each child its own flex item,
    // and an overflow:hidden card then clips instead of the page scrolling.
    <Box sx={{ display: "flex", flexDirection: "column" }}>
      <PageHeader
        title="Dashboard"
        subtitle="Registrations, lifecycle spread and the work in flight"
        actions={<PeriodSelect value={days} onChange={setDays} />}
      />

      {isPending || !data ? (
        <LoadingBlock height={120} />
      ) : (
        <Grid container spacing={2} mb={3}>
          <Grid size={{ xs: 12, sm: 6, md: 3 }}>
            <StatTile
              label="Organisations"
              value={data.summary.totalOrganizations}
              hint="Customers registered through PLG"
              onClick={() => navigate("/plg/organizations")}
            />
          </Grid>
          <Grid size={{ xs: 12, sm: 6, md: 3 }}>
            <StatTile
              label="Registrations"
              value={data.summary.totalRegistrations}
              hint="One per organisation and platform"
              onClick={() => navigate("/plg/organizations")}
            />
          </Grid>
          <Grid size={{ xs: 12, sm: 6, md: 3 }}>
            <StatTile
              label="Awaiting acknowledgement"
              value={data.summary.newRegistrations}
              hint="Nobody has picked these up yet"
              color={data.summary.newRegistrations > 0 ? "warning.main" : "text.primary"}
              onClick={() => navigate("/plg/new-registrations")}
            />
          </Grid>
          <Grid size={{ xs: 12, sm: 6, md: 3 }}>
            <StatTile
              label="Pairings needing attention"
              value={data.summary.pairingsNeedingAttention}
              hint="Acknowledged, and not yet at an ending stage"
              // mine=false, because this counts the whole team's queue. A tile
              // should land you on a page showing the number it just showed you.
              onClick={() => navigate("/plg/work-queue?mine=false")}
            />
          </Grid>
        </Grid>
      )}

      <DashboardCharts data={data} />
    </Box>
  );
}
