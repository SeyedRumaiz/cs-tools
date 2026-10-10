import { Grid, Stack, Typography } from "@wso2/oxygen-ui";
import type { JSX } from "react";

import { DonutChart, MultiLineChart } from "@features/plg/components/charts";
import { LoadingBlock, SectionCard } from "@features/plg/components/common";
import type { DashboardAnalytics } from "@features/plg/api/types";

/**
 * The cohort charts, shared by both dashboards.
 *
 * These are the part of the two pages that genuinely is one thing: the funnel
 * is the funnel whoever is looking at it, and the day its shape changes it has
 * to change in both places. The tiles above are not like that — they answer
 * different questions for different readers — which is why only this half is
 * shared and the pages themselves are separate.
 *
 * `data` is optional so each card can show its own loading block rather than
 * the page blanking: the charts arrive together, but the layout should not
 * jump when they do.
 */
export function DashboardCharts({ data }: { data?: DashboardAnalytics }): JSX.Element {
  return (
    <Stack spacing={2}>
      <Grid container spacing={2}>
        <Grid size={{ xs: 12, md: 4 }}>
          <SectionCard fill title="Registrations by platform">
            {data ? <DonutChart data={data.registrationsByProduct} /> : <LoadingBlock />}
          </SectionCard>
        </Grid>
        <Grid size={{ xs: 12, md: 4 }}>
          <SectionCard fill title="Pairings by lifecycle stage">
            {data ? <DonutChart data={data.registrationsByLifecycleStage} humanize /> : <LoadingBlock />}
          </SectionCard>
        </Grid>
        <Grid size={{ xs: 12, md: 4 }}>
          <SectionCard
            fill
            title="Subscription mix"
            action={
              data && data.summary.trialsEndingSoon > 0 ? (
                <Typography variant="caption" color="warning.main">
                  {data.summary.trialsEndingSoon} trials ending within 30 days
                </Typography>
              ) : undefined
            }
          >
            {data ? <DonutChart data={data.subscriptionMix} humanize /> : <LoadingBlock />}
          </SectionCard>
        </Grid>
      </Grid>

      <SectionCard title="Registrations over time">
        {data ? <MultiLineChart data={data.registrationsOverTime} /> : <LoadingBlock height={300} />}
      </SectionCard>
    </Stack>
  );
}
