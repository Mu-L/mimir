---
aliases:
  - ../../../operators-guide/monitor-grafana-mimir/dashboards/reads-resources/
  - ../../../operators-guide/monitoring-grafana-mimir/dashboards/reads-resources/
  - ../../../operators-guide/visualizing-metrics/dashboards/reads-resources/
description: View an example Reads resources dashboard.
menuTitle: Reads resources
title: Grafana Mimir Reads resources dashboard
weight: 110
---

<!-- Note: This topic is mounted in the GEM documentation. Ensure that all updates are also applicable to GEM. -->

# Grafana Mimir Reads resources dashboard

The Reads resources dashboard shows CPU, memory, disk, and other resources utilization metrics.
The dashboard isolates each service on the read path into its own section and displays the order in which a read request flows.

This dashboard requires [additional resources metrics](../../requirements/#additional-resources-metrics).

## Example

The following example shows a Reads resources dashboard from a demo cluster.

![Grafana Mimir reads resources dashboard](mimir-reads-resources.png)
