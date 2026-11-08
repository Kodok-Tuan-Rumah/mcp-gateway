const state = {
  reportId: '',
  shareToken: '',
  readOnly: false,
};

const byId = (id) => document.getElementById(id);
const supportedDecisions = new Set(['answer', 'answered', 'supported', 'support']);

function message(text, isError = false) {
  const node = byId('message');
  node.textContent = text;
  node.classList.toggle('error', isError);
}

function headersFor(json = false) {
  const headers = new Headers();
  if (!state.readOnly) {
    const key = byId('api-key').value.trim();
    if (!key) throw new Error('Enter an API key to create or access a private report.');
    headers.set('Authorization', `Bearer ${key}`);
  }
  if (json) headers.set('Content-Type', 'application/json');
  return headers;
}

async function fetchJSON(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: headersFor(Boolean(options.body)),
  });
  let data;
  try {
    data = await response.json();
  } catch {
    data = {};
  }
  if (!response.ok) {
    throw new Error(data.error || `Request failed (${response.status}).`);
  }
  return data;
}

function valueText(value) {
  if (typeof value === 'string') return value;
  if (value === null || value === undefined) return '';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function evidenceItems(value) {
  if (Array.isArray(value)) return value;
  return value === null || value === undefined ? [] : [value];
}

function evidenceContent(item) {
  if (typeof item === 'string') return item.trim();
  if (!item || typeof item !== 'object') return '';
  for (const key of ['text', 'content', 'snippet', 'excerpt', 'passage']) {
    if (typeof item[key] === 'string' && item[key].trim()) return item[key].trim();
  }
  return '';
}

function evidenceSource(item) {
  if (!item || typeof item !== 'object') return '';
  for (const key of ['source_id', 'source', 'document_id', 'doc_id']) {
    if (typeof item[key] === 'string' && item[key].trim()) return item[key].trim();
  }
  return '';
}

function addText(parent, tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = text;
  parent.appendChild(node);
  return node;
}

function renderEvidence(parent, items) {
  if (!items.length) {
    addText(parent, 'p', 'no-evidence', 'No textual evidence was returned.');
    return false;
  }

  const list = document.createElement('ul');
  list.className = 'evidence-list';
  let hasGroundedEvidence = false;
  for (const item of items) {
    const li = document.createElement('li');
    const source = evidenceSource(item);
    if (source) addText(li, 'strong', 'source-id', String(source));
    const text = evidenceContent(item);
    if (text) {
      if (source) hasGroundedEvidence = true;
      addText(li, 'p', 'evidence-text', text);
    } else {
      addText(li, 'pre', 'evidence-raw', valueText(item));
    }
    if (item && typeof item === 'object' && item.score !== undefined) {
      addText(li, 'small', 'evidence-score', `Score: ${valueText(item.score)}`);
    }
    list.appendChild(li);
  }
  parent.appendChild(list);
  return hasGroundedEvidence;
}

function renderReport(report) {
  state.reportId = report.report_id || '';
  byId('report-id').textContent = state.reportId || 'Shared report';
  byId('report-status').textContent = `Status: ${report.status || 'unknown'}`;
  byId('report-summary').textContent = valueText(report.summary);
  byId('report-meta').hidden = false;
  byId('report-actions').hidden = state.readOnly;
  byId('claims').replaceChildren();

  const claims = Array.isArray(report.claims) ? report.claims : [];
  if (!claims.length) {
    addText(byId('claims'), 'p', 'no-evidence', 'This report contains no claims.');
    return;
  }

  for (const claim of claims) {
    const card = document.createElement('article');
    const zone = String(claim.confidence_zone || 'red').toLowerCase();
    card.className = `claim-card zone-${['green', 'yellow', 'red'].includes(zone) ? zone : 'red'}`;

    const comparison = document.createElement('div');
    comparison.className = 'comparison';
    const claimSide = document.createElement('section');
    claimSide.className = 'claim-side';
    const evidenceSide = document.createElement('section');
    evidenceSide.className = 'evidence-side';

    addText(claimSide, 'h3', '', `Claim ${Number(claim.claim_index ?? 0) + 1}`);
    addText(claimSide, 'p', 'claim-text', String(claim.claim || ''));
    const decision = String(claim.decision || 'unknown');
    addText(claimSide, 'p', 'decision', `Decision: ${decision}`);
    addText(claimSide, 'p', 'zone', `Confidence zone: ${zone}`);
    if (claim.calibrated_confidence !== undefined) {
      addText(claimSide, 'p', 'confidence', `Confidence: ${valueText(claim.calibrated_confidence)}`);
    }

    addText(evidenceSide, 'h3', '', 'Evidence');
    const hasGroundedEvidence = renderEvidence(evidenceSide, evidenceItems(claim.evidence));
    if (!hasGroundedEvidence) {
      const notice = supportedDecisions.has(decision.toLowerCase())
        ? 'Grounding check failed: this answer has no sourced evidence and must not be treated as supported.'
        : 'No sourced supporting evidence; this result is an abstention, not a verified claim.';
      addText(evidenceSide, 'p', 'grounding-warning', notice);
    }
    if (claim.span_attribution) {
      addText(evidenceSide, 'h4', '', 'Span attribution');
      addText(evidenceSide, 'pre', 'attribution', valueText(claim.span_attribution));
    }

    comparison.append(claimSide, evidenceSide);
    card.appendChild(comparison);
    byId('claims').appendChild(card);
  }

  byId('export-md').disabled = state.readOnly;
  byId('export-json').disabled = state.readOnly;
  byId('export-pdf').disabled = state.readOnly;
  byId('revoke-share').hidden = state.readOnly || !state.shareToken;
  byId('copy-share').hidden = state.readOnly || !state.shareToken;
}

async function createReport() {
  const claims = byId('claims-input').value.split('\n').map((claim) => claim.trim()).filter(Boolean);
  if (!claims.length) throw new Error('Enter at least one claim, one per line.');

  state.readOnly = false;
  message('Creating report...');
  const created = await fetchJSON('/reports', {
    method: 'POST',
    body: JSON.stringify({ claims }),
  });
  if (!created.report_id) throw new Error('The API did not return a report ID.');
  state.reportId = created.report_id;
  state.shareToken = created.share_token || '';
  const report = await fetchJSON(`/reports/${encodeURIComponent(state.reportId)}`);
  renderReport(report);
  message(`Report ${state.reportId} loaded. Status: ${created.status || report.status || 'unknown'}.`);
}

async function exportReport(format) {
  if (!state.reportId) throw new Error('Create or load a report first.');
  const response = await fetch(`/reports/${encodeURIComponent(state.reportId)}/export.${format}`, {
    headers: headersFor(),
  });
  if (!response.ok) throw new Error(`Export failed (${response.status}).`);
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = `audit-report-${state.reportId}.${format}`;
  link.click();
  URL.revokeObjectURL(url);
}

async function copyShareLink() {
  if (!state.shareToken) throw new Error('This report has no active share token.');
  const url = new URL('/audit/', window.location.origin);
  url.searchParams.set('share', state.shareToken);
  await navigator.clipboard.writeText(url.toString());
  message('Read-only share link copied. It expires or can be revoked.');
}

async function revokeShare() {
  if (!state.reportId || !state.shareToken) throw new Error('There is no active share token.');
  if (!window.confirm('Revoke the public share link?')) return;
  await fetchJSON(`/reports/${encodeURIComponent(state.reportId)}/revoke`, { method: 'POST' });
  state.shareToken = '';
  byId('copy-share').hidden = true;
  byId('revoke-share').hidden = true;
  message('Share link revoked.');
}

async function loadSharedReport(token) {
  state.readOnly = true;
  byId('private-controls').hidden = true;
  byId('share-view-note').hidden = false;
  const report = await fetchJSON(`/r/${encodeURIComponent(token)}`);
  renderReport(report);
  message('Read-only shared report.');
}

function bindAction(id, action) {
  byId(id).addEventListener('click', async () => {
    const button = byId(id);
    button.disabled = true;
    try {
      await action();
    } catch (error) {
      message(error.message || 'Request failed.', true);
    } finally {
      button.disabled = false;
    }
  });
}

bindAction('run', createReport);
bindAction('export-md', () => exportReport('md'));
bindAction('export-json', () => exportReport('json'));
bindAction('export-pdf', () => exportReport('pdf'));
bindAction('copy-share', copyShareLink);
bindAction('revoke-share', revokeShare);

const sharedToken = new URLSearchParams(window.location.search).get('share');
if (sharedToken) {
  loadSharedReport(sharedToken).catch((error) => message(error.message || 'Unable to load shared report.', true));
}
