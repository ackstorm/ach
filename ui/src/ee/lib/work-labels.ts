// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
// work-labels.ts — human labels and domain colour class for the classifier's
// taxonomy v3 categories; Overview (Work classification) and the trace drawer
// (Work category) share them. Colour classes are .wc-c-* / .wc-d-* in
// components/analytics/reports.css.
export const WORK_LABEL: Record<string, string> = {
  'technology.software_engineering': 'Software engineering', 'technology.infrastructure_ops': 'Infrastructure & ops', 'technology.security': 'Security', 'technology.genai_development': 'GenAI development',
  'data.analytics': 'Data & analytics', 'sales.proposals': 'Sales proposals', 'sales.customer_support': 'Customer support', 'marketing.content_design': 'Marketing & content',
  'business.operations': 'Business operations', 'general.research_learning': 'Research & learning', 'general.personal': 'Personal', other: 'Other', trivial: 'Trivial',
};
export const workLabel = (category: string) => WORK_LABEL[category] ?? category;
export const workDomain = (category: string, domain?: string | null) => domain || category.split('.')[0];
// One colour per category (.wc-c-* in reports.css), so two categories of one
// domain never share a hue; an unknown category falls back to its domain's.
export const workClass = (category: string, domain?: string | null) => category in WORK_LABEL ? `wc-c-${category.replace('.', '-')}` : `wc-d-${workDomain(category, domain)}`;
