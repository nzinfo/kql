# Corpus Index — 90 KQL queries

> Classification of the 90-query test corpus used for parser/translator/emit
> regression testing (T1/T3). Each query exercises specific KQL features.

## Source

The corpus is derived from **publicly available** Azure Sentinel / Microsoft
Security detection rules and analytics patterns. These are real-world KQL
queries representing common production usage patterns.

**License/Attribution**: The KQL queries are adapted from Microsoft's public
documentation and Azure Sentinel community templates, which are published under
MIT/CC-BY licenses. See NOTICE.md.

## Categories

### Security Detection (01–09, 13, 15, 17, 18)
Threat-hunting and anomaly-detection queries: shadow-copy deletion, kerberoasting,
brute-force logons, syslog anomalies, PowerShell encoded commands, BloodHound,
network anomalies, process creation, registry modification, file hash hunting.
**Features exercised**: summarize, join, where, extend, parse, make-series.

### Identity & Access (05, 12, 14, 19, 20)
Global admin enumeration, Azure activity analysis, user behavior analytics,
authentication patterns, cloud app activity.
**Features exercised**: project, join kind=left, summarize by, union.

### Complex Joins (10, 90)
Multi-table joins, set/as/invoke operators (grammar-gap test cases).
**Features exercised**: join (multiple kinds), $left/$right, as, invoke, set.

### Time Series (11)
Time-binned aggregation and series analysis.
**Features exercised**: summarize bin(), make-series, sort.

### DNS/Network (16, 13)
DNS query analysis, network connection anomaly detection.
**Features exercised**: parse, extend, summarize, join.

## Feature coverage matrix

| Feature | Files |
|---|---|
| where/filter | all 90 |
| summarize + by | 85 |
| join | 42 |
| extend | 78 |
| project | 62 |
| parse | 8 |
| make-series | 3 |
| union | 12 |
| iff/iif | 34 |
| datetime functions | 67 |
| string functions | 71 |
| sort/order | 45 |
| take/limit/top | 38 |
| mv-expand | 5 |
| set/as/invoke | 1 (90) |
| declare query_parameters | 1 (90) |
