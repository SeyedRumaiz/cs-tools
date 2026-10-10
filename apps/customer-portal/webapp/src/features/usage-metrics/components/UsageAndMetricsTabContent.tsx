// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import { Box, Button, IconButton, Typography, useTheme } from "@wso2/oxygen-ui";
import { ArrowLeft, ArrowRight, Server, Upload } from "@wso2/oxygen-ui-icons-react";
import type { ReactNode } from "react";
import type { JSX } from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { USAGE_METRICS_DEPLOYMENT_TAB_PREFIX } from "@features/usage-metrics/constants/usageMetricsConstants";
import { useParams } from "react-router";
import TabBar from "@components/tab-bar/TabBar";
import UsageEnvironmentProductsPanel from "@features/usage-metrics/components/UsageEnvironmentProductsPanel";
import UsageMetricsTimeRangeSelector from "@features/usage-metrics/components/UsageMetricsTimeRangeSelector";
import DeploymentUsageUploadDialog from "@features/usage-metrics/components/DeploymentUsageUploadDialog";
import { UsageTimeRange } from "@features/project-details/types/usage";
import {
  getActiveUsageDeploymentId,
  resolveUsagePresetDateRange,
} from "@features/usage-metrics/utils/usageMetricsTab";
import { usePostProjectDeploymentsSearchAll } from "@api/usePostProjectDeploymentsSearch";
import { getUsageOverviewAccentForTypeId } from "@features/usage-metrics/utils/usageMetricsAccent";

/**
 * Usage & Metrics area: time range, inner environment tabs, overview and product drill-downs.
 *
 * @returns {JSX.Element} Full usage metrics experience for project details.
 */
export default function UsageAndMetricsTabContent(): JSX.Element {
  const { projectId } = useParams<{ projectId: string }>();
  const theme = useTheme();
  const [timeRange, setTimeRange] = useState<UsageTimeRange>(
    UsageTimeRange.ONE_MONTH,
  );
  const [innerTab, setInnerTab] = useState<string>("");
  const [expandedProductIds, setExpandedProductIds] = useState<Set<string>>(
    () => new Set(),
  );

  // The deployment tab strip scrolls horizontally once there are more
  // deployments than fit (see the scroll container below) but, without
  // these, gives no visual sign that there's more to see -- the last tab
  // just looks abruptly clipped against the Upload button (a real,
  // reported bug: digiops-cs#3241). canScrollLeft/Right drive a fade mask
  // plus a scroll-by-one-page arrow button on whichever edge(s) still have
  // hidden tabs.
  const tabScrollRef = useRef<HTMLDivElement | null>(null);
  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);

  const updateTabScrollAffordance = useCallback(() => {
    const el = tabScrollRef.current;
    if (!el) return;
    // 1px tolerance: scrollWidth/clientWidth can disagree by a sub-pixel
    // rounding amount even when fully scrolled, which would otherwise leave
    // a phantom arrow/fade visible at the resting position.
    setCanScrollLeft(el.scrollLeft > 1);
    setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);

  const scrollTabsBy = useCallback((direction: 1 | -1) => {
    const el = tabScrollRef.current;
    if (!el) return;
    el.scrollBy({ left: direction * el.clientWidth * 0.8, behavior: "smooth" });
  }, []);

  const [customStart, setCustomStart] = useState<string>("");
  const [customEnd, setCustomEnd] = useState<string>("");
  const [appliedCustomStart, setAppliedCustomStart] = useState<string>("");
  const [appliedCustomEnd, setAppliedCustomEnd] = useState<string>("");
  const [uploadOpen, setUploadOpen] = useState<boolean>(false);

  const dateRange = useMemo(() => {
    if (timeRange === UsageTimeRange.CUSTOM && appliedCustomStart && appliedCustomEnd) {
      return { startDate: appliedCustomStart, endDate: appliedCustomEnd };
    }
    return resolveUsagePresetDateRange(timeRange);
  }, [timeRange, appliedCustomStart, appliedCustomEnd]);

  const { data: deploymentsData } = usePostProjectDeploymentsSearchAll(
    projectId ?? "",
  );

  // Every active deployment gets a tab, regardless of productCount -- a
  // deployment with zero deployed products still legitimately shows a
  // "no products in this environment" empty state inside
  // UsageEnvironmentProductsPanel, and staging's own tab bar confirms this:
  // it lists deployments with productCount 0 (e.g. a QA/test deployment)
  // right alongside ones with real products. Filtering here just hid tabs a
  // customer should still be able to click into.
  const deploymentTabs = useMemo(
    () =>
      (deploymentsData ?? []).map((dep) => ({
        id: `${USAGE_METRICS_DEPLOYMENT_TAB_PREFIX}${dep.id}`,
        label: dep.name,
        icon: Server,
        iconColor: getUsageOverviewAccentForTypeId(dep.type.id).iconColor,
        instanceCount: dep.instanceCount ?? 0,
        productCount: dep.productCount ?? 0,
      })),
    [deploymentsData],
  );

  // Re-check once the actual tab buttons have rendered (deploymentTabs
  // arrives asynchronously), on window resize, and when the scroller itself
  // resizes. The latter covers layout changes such as expanding the sidebar.
  useEffect(() => {
    updateTabScrollAffordance();
    window.addEventListener("resize", updateTabScrollAffordance);
    const scroller = tabScrollRef.current;
    const resizeObserver = typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver(updateTabScrollAffordance);
    if (scroller) resizeObserver?.observe(scroller);

    return () => {
      window.removeEventListener("resize", updateTabScrollAffordance);
      resizeObserver?.disconnect();
    };
  }, [deploymentTabs, updateTabScrollAffordance]);

  const defaultTab = deploymentTabs[0]?.id ?? "";

  const activeTab = innerTab || defaultTab;

  const activeDeploymentId = useMemo(
    () => getActiveUsageDeploymentId(activeTab),
    [activeTab],
  );

  const activeDeployment = useMemo(
    () => (deploymentsData ?? []).find((dep) => dep.id === activeDeploymentId),
    [deploymentsData, activeDeploymentId],
  );

  const toggleProduct = useCallback((productId: string) => {
    setExpandedProductIds((prev) => {
      const next = new Set(prev);
      if (next.has(productId)) {
        next.delete(productId);
      } else {
        next.add(productId);
      }
      return next;
    });
  }, []);

  const clearCustomApplied = useCallback(() => {
    setAppliedCustomStart("");
    setAppliedCustomEnd("");
  }, []);

  const handleApplyCustom = () => {
    setAppliedCustomStart(customStart);
    setAppliedCustomEnd(customEnd);
  };

  const handleCancelCustom = () => {
    setCustomStart(appliedCustomStart);
    setCustomEnd(appliedCustomEnd);
    setTimeRange(UsageTimeRange.ONE_MONTH);
  };

  const uploadButton: ReactNode = (
    <Button
      variant="outlined"
      size="small"
      startIcon={<Upload size={16} />}
      onClick={() => setUploadOpen(true)}
      sx={{ flexShrink: 0 }}
    >
      Upload
    </Button>
  );

  const timeRangeSelector = (
    <UsageMetricsTimeRangeSelector
      innerTab={activeTab}
      timeRange={timeRange}
      onTimeRangeChange={setTimeRange}
      onClearCustomApplied={clearCustomApplied}
      customStart={customStart}
      customEnd={customEnd}
      onCustomStartChange={setCustomStart}
      onCustomEndChange={setCustomEnd}
      onApplyCustom={handleApplyCustom}
      onCancelCustom={handleCancelCustom}
      appliedCustomStart={appliedCustomStart}
      appliedCustomEnd={appliedCustomEnd}
    />
  );

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 2,
        width: "100%",
        maxWidth: "100%",
        minWidth: 0,
        boxSizing: "border-box",
        overflowX: "hidden",
      }}
    >
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          gap: 1.5,
          width: "100%",
          maxWidth: "100%",
          minWidth: 0,
        }}
      >
        <Box
          sx={{
            position: "relative",
            flex: 1,
            minWidth: 0,
            overflow: "hidden",
            contain: "inline-size",
          }}
        >
          <Box
            ref={tabScrollRef}
            onScroll={updateTabScrollAffordance}
            sx={{
              overflowX: "auto",
              overflowY: "hidden",
              scrollbarWidth: "none",
              "&::-webkit-scrollbar": { display: "none" },
            }}
          >
            <Box sx={{ width: "max-content" }}>
              <TabBar
                tabs={deploymentTabs}
                activeTab={activeTab}
                onTabChange={setInnerTab}
                keepButtonWidth={true}
                compact={true}
                sx={{ mb: 0, border: "none", boxShadow: "none" }}
              />
            </Box>
          </Box>
          {/* Fade + arrow on each edge that still hides a tab, so a cut-off
              tab reads as "more to scroll to" instead of a hard clip.
              Uses CSS variable --oxygen-palette-background-paper so the
              fade seamlessly adapts to both light and dark color schemes. */}
          {canScrollLeft && (
            <Box
              sx={{
                position: "absolute",
                left: 0,
                top: 0,
                bottom: 0,
                display: "flex",
                alignItems: "center",
                pr: 2,
                background: `linear-gradient(to right, var(--oxygen-palette-background-paper, ${theme.palette.background.paper}) 40%, transparent)`,
              }}
            >
              <IconButton
                size="small"
                aria-label="Scroll deployment tabs left"
                onClick={() => scrollTabsBy(-1)}
                sx={{ bgcolor: "background.paper", boxShadow: 1, "&:hover": { bgcolor: "background.paper" } }}
              >
                <ArrowLeft size={14} />
              </IconButton>
            </Box>
          )}
          {canScrollRight && (
            <Box
              sx={{
                position: "absolute",
                right: 0,
                top: 0,
                bottom: 0,
                display: "flex",
                alignItems: "center",
                pl: 2,
                background: `linear-gradient(to left, var(--oxygen-palette-background-paper, ${theme.palette.background.paper}) 40%, transparent)`,
              }}
            >
              <IconButton
                size="small"
                aria-label="Scroll deployment tabs right"
                onClick={() => scrollTabsBy(1)}
                sx={{ bgcolor: "background.paper", boxShadow: 1, "&:hover": { bgcolor: "background.paper" } }}
              >
                <ArrowRight size={14} />
              </IconButton>
            </Box>
          )}
        </Box>
        {uploadButton}
      </Box>

      {activeDeployment && (
        <Typography variant="body2" color="text.secondary">
          {activeDeployment.name}
          {activeDeployment.type?.label && ` · ${activeDeployment.type.label}`}
        </Typography>
      )}

      <DeploymentUsageUploadDialog
        open={uploadOpen}
        onClose={() => setUploadOpen(false)}
      />

      {timeRangeSelector}

      {activeDeploymentId != null && (
        <UsageEnvironmentProductsPanel
          deploymentId={activeDeploymentId}
          projectId={projectId}
          dateRange={dateRange}
          expandedProductIds={expandedProductIds}
          onToggleProduct={toggleProduct}
        />
      )}
    </Box>
  );
}
