/**
 * Content Filtering panel of the source modal.
 *
 * Three stages, evaluated in this order by the server: the content type gate,
 * the group picker (fed by the source's real group inventory) and the
 * name/URL patterns. A preview runs the draft rules against the provider's
 * catalog and reports what would be kept before anything is saved.
 */

const FILTER_TYPES = ['live', 'vod', 'series'];
const FILTER_TYPE_LABEL = { live: 'Live', vod: 'VOD', series: 'Series' };
const FILTER_TYPE_DOT = { live: 'bg-green-600', vod: 'bg-orange-600', series: 'bg-kptv-blue' };
const FILTER_REGEX_IDS = [
    'source-group-filter-regex',
    'source-live-category-regex', 'source-vod-category-regex', 'source-series-category-regex',
    'source-live-include-regex', 'source-live-exclude-regex',
    'source-series-include-regex', 'source-series-exclude-regex',
    'source-vod-include-regex', 'source-vod-exclude-regex',
];

/** Panel state, replaced every time the source modal opens. */
let filterPanel = null;
let filterPreviewTimer = null;
let sourceRuleEditor = null;

/**
 * Mirrors the server's group key: lowercased, trimmed, whitespace collapsed,
 * so a selection survives a provider changing "♦️  GLOBO" to "♦️ Globo".
 * @param {string} name
 * @returns {string}
 */
function filterGroupKey(name) {
    return String(name || '').toLowerCase().trim().split(/\s+/).filter(Boolean).join(' ');
}

/**
 * Builds a fresh panel state.
 * @returns {Object}
 */
function newFilterPanelState() {
    return {
        groups: [],              // [{ key, name, type, total, kept, samples }] from inventory or preview
        selected: new Map(),     // key -> name as the provider spells it
        overrides: new Map(),    // key -> forced content type
        mode: '',                // '', 'include' or 'exclude'
        types: new Set(FILTER_TYPES),
        search: '',
        typeChip: '',
        report: null,            // last preview response
        profileRules: [],        // rules inherited from the selected profile
        inventoryAt: null,       // when the inventory shown was recorded
        loading: false,
        pending: false,          // a rule changed while a preview was in flight
    };
}

/**
 * Wires the panel's controls once at startup.
 */
function initFilterPanel() {
    sourceRuleEditor = createRuleEditor('source-rules', { onChange: () => { renderFilterDefaultHint(); scheduleFilterPreview(); } });
    document.getElementById('source-rules').addEventListener('change', () => renderFilterDefaultHint());

    // a new rule always keeps: guessing from the default surprises the operator
    document.getElementById('source-add-rule-btn').addEventListener('click', () => {
        sourceRuleEditor.addRule({ field: 'group', action: 'include' });
    });
    document.getElementById('source-filter-default').addEventListener('click', (e) => {
        const btn = e.target.closest('.seg-btn');
        if (!btn) return;
        document.querySelectorAll('#source-filter-default .seg-btn').forEach(b => b.classList.toggle('active', b === btn));
        renderFilterDefaultHint();
        scheduleFilterPreview();
    });
    document.getElementById('source-filter-profile').addEventListener('change', () => {
        applySourceProfile();
        scheduleFilterPreview();
    });

    document.getElementById('filter-preview-btn').addEventListener('click', () => runFilterPreview(false));
    document.getElementById('filter-refetch-btn').addEventListener('click', () => runFilterPreview(true));

    document.getElementById('filter-type-toggles').addEventListener('click', (e) => {
        const btn = e.target.closest('.type-toggle');
        if (!btn || !filterPanel) return;
        const type = btn.dataset.type;
        if (filterPanel.types.has(type)) {
            if (filterPanel.types.size === 1) {
                showNotification('At least one content type must stay enabled', 'warning');
                return;
            }
            filterPanel.types.delete(type);
        } else {
            filterPanel.types.add(type);
        }
        renderFilterPanel();
        scheduleFilterPreview();
    });

    document.getElementById('filter-group-mode').addEventListener('click', (e) => {
        const btn = e.target.closest('.seg-btn');
        if (!btn || !filterPanel) return;
        filterPanel.mode = btn.dataset.mode;
        renderFilterPanel();
        scheduleFilterPreview();
    });

    document.getElementById('filter-group-search').addEventListener('input', (e) => {
        if (!filterPanel) return;
        // not trimmed: a trailing space is part of a prefix search
        filterPanel.search = e.target.value.toLowerCase();
        renderFilterGroups();
    });

    document.getElementById('filter-group-type-chips').addEventListener('click', (e) => {
        const chip = e.target.closest('.chip');
        if (!chip || !filterPanel) return;
        filterPanel.typeChip = chip.dataset.type;
        renderFilterGroups();
    });

    document.getElementById('filter-group-select-visible').addEventListener('click', () => {
        if (!filterPanel) return;
        visibleFilterGroups().forEach(g => filterPanel.selected.set(g.key, g.name));
        if (filterPanel.mode === '') filterPanel.mode = 'include';
        renderFilterPanel();
        scheduleFilterPreview();
    });

    document.getElementById('filter-group-clear').addEventListener('click', () => {
        if (!filterPanel) return;
        filterPanel.selected.clear();
        renderFilterPanel();
        scheduleFilterPreview();
    });

    const list = document.getElementById('filter-group-list');
    list.addEventListener('change', (e) => {
        if (!filterPanel) return;
        const key = e.target.dataset.key;
        if (e.target.classList.contains('group-check')) {
            toggleFilterGroup(key, e.target.checked);
        } else if (e.target.classList.contains('group-type')) {
            if (e.target.value) filterPanel.overrides.set(key, e.target.value);
            else filterPanel.overrides.delete(key);
            renderFilterPanel();
            scheduleFilterPreview();
        }
    });
    list.addEventListener('click', (e) => {
        if (!filterPanel) return;
        const ruleBtn = e.target.closest('.group-rule');
        if (ruleBtn) {
            const row = ruleBtn.closest('.group-row');
            const group = filterPanel.groups.find(g => g.key === row.dataset.key);
            addRuleForGroup(group ? group.name : row.dataset.key, ruleBtn.dataset.action);
            return;
        }
        // the whole row toggles, except its own controls
        if (e.target.closest('input, select, button')) return;
        const row = e.target.closest('.group-row');
        if (!row) return;
        const key = row.dataset.key;
        toggleFilterGroup(key, !filterPanel.selected.has(key));
    });

    FILTER_REGEX_IDS.forEach(id => {
        const input = document.getElementById(id);
        input.addEventListener('input', () => validateRegexInput(input));
        input.addEventListener('change', () => scheduleFilterPreview());
    });
}

/** @returns {string} the verdict for a stream no rule matched */
function filterDefaultAction() {
    const active = document.querySelector('#source-filter-default .seg-btn.active');
    return active ? active.dataset.default : 'keep';
}

/**
 * Appends a rule matching exactly one group label, so a row of the picker can
 * become a rule without retyping an emoji-laden name.
 * @param {string} label - the provider's group label
 * @param {string} action - include or exclude
 */
function addRuleForGroup(label, action) {
    sourceRuleEditor.addRule({
        field: 'group',
        action,
        pattern: label ? `^${escapeRegex(label)}$` : '^$',
        note: label || '(no group)',
    });
    // an include rule only means something when the rest is dropped
    if (action === 'include' && filterDefaultAction() !== 'drop' && sourceRuleEditor.getRules().length === 1) {
        document.querySelector('#source-filter-default .seg-btn[data-default="drop"]').click();
    }
}

/** Explains what the current default does, in the terms of the rules above. */
function renderFilterDefaultHint() {
    const hint = document.getElementById('source-filter-default-hint');
    if (!hint) return;

    const own = sourceRuleEditor ? sourceRuleEditor.getRules() : [];
    const inherited = (filterPanel && filterPanel.profileRules) || [];
    const all = inherited.concat(own);
    const keeps = all.filter(r => r.action === 'include').length;

    let text = '';
    let warn = false;
    if (filterDefaultAction() === 'drop') {
        warn = keeps === 0;
        text = warn
            ? 'Nothing would be imported: dropping by default with no Keep rule leaves nothing.'
            : 'Only what a Keep rule matches is imported — the rules are an allow list.';
    } else if (all.length) {
        text = 'Everything is imported except what a Drop rule matches — the rules are a deny list.';
    }

    hint.textContent = text;
    hint.classList.toggle('text-orange-400', warn);
    hint.classList.toggle('text-gray-400', !warn);
}

/**
 * Reflects the selected profile: its rules are shown above the source's own,
 * read-only, because they are shared with every other source using it.
 */
function applySourceProfile() {
    if (!filterPanel) return;
    const name = document.getElementById('source-filter-profile').value;
    const profile = (typeof allFilterProfiles !== 'undefined' ? allFilterProfiles : []).find(p => p.name === name);
    filterPanel.profileRules = profile ? profile.rules : [];
    sourceRuleEditor.setProfileRules(name, filterPanel.profileRules);
    renderFilterDefaultHint();
}

/**
 * Selects or deselects a group. Ticking a group while every group is imported
 * switches to "only selected", since that is what the tick means.
 * @param {string} key
 * @param {boolean} on
 */
function toggleFilterGroup(key, on) {
    const known = filterPanel.groups.find(g => g.key === key);
    if (on) {
        filterPanel.selected.set(key, known ? known.name : (filterPanel.selected.get(key) || key));
        if (filterPanel.mode === '') filterPanel.mode = 'include';
    } else {
        filterPanel.selected.delete(key);
    }
    renderFilterPanel();
    scheduleFilterPreview();
}

/**
 * Resets the panel for a source being edited, or for a new source when null,
 * and loads the group inventory recorded by the source's last import.
 * @param {Object|null} source
 */
function resetFilterPanel(source) {
    clearTimeout(filterPreviewTimer);
    filterPanel = newFilterPanelState();

    if (source) {
        filterPanel.mode = source.groupFilterMode || '';
        (source.groupFilterList || []).forEach(name => filterPanel.selected.set(filterGroupKey(name), name));
        Object.entries(source.groupTypeOverrides || {}).forEach(([name, type]) => filterPanel.overrides.set(filterGroupKey(name), type));
        if (source.importTypes && source.importTypes.length) {
            filterPanel.types = new Set(source.importTypes);
        }
    }

    renderProfileOptions();
    document.getElementById('source-filter-profile').value = (source && source.filterProfile) || '';
    document.querySelectorAll('#source-filter-default .seg-btn').forEach(b => {
        b.classList.toggle('active', b.dataset.default === ((source && source.filterDefault) || 'keep'));
    });
    sourceRuleEditor.setRules((source && source.filterRules) || []);
    applySourceProfile();

    document.getElementById('source-group-filter-regex').value = (source && source.groupFilterRegex) || '';
    document.getElementById('filter-group-search').value = '';
    document.getElementById('filter-group-regex-details').open = !!(source && source.groupFilterRegex);
    document.getElementById('filter-patterns-details').open = FILTER_REGEX_IDS.slice(1)
        .some(id => document.getElementById(id).value.trim() !== '');
    FILTER_REGEX_IDS.forEach(id => validateRegexInput(document.getElementById(id)));

    renderFilterPanel();

    if (source && source.url) {
        loadFilterInventory(source.url);
    }
}

/**
 * Loads the inventory recorded by the last import so the picker shows real
 * provider labels the moment the modal opens.
 * @param {string} url
 */
async function loadFilterInventory(url) {
    const panel = filterPanel;
    try {
        const data = await apiCall('/api/sources/groups?url=' + encodeURIComponent(url), { quiet: true });
        if (panel !== filterPanel) return; // modal was reopened meanwhile
        setFilterGroups(data.groups || []);
        filterPanel.inventoryAt = data.import ? data.import.lastImportAt : null;
        renderFilterPanel();
    } catch (error) {
        // a source that never imported has no inventory yet; the preview fills it
    }
}

/**
 * Replaces the group list, keying every entry the way the server does.
 * @param {Array<Object>} groups
 */
function setFilterGroups(groups) {
    filterPanel.groups = groups.map(g => ({
        key: filterGroupKey(g.name),
        name: g.name || '',
        type: g.type || 'live',
        total: g.total || 0,
        kept: g.kept || 0,
        samples: g.samples || [],
    }));
}

/**
 * Returns the filter fields of the draft source, in the shape the API stores.
 * @returns {Object}
 */
function collectFilterFields() {
    const panel = filterPanel || newFilterPanelState();
    const overrides = {};
    panel.overrides.forEach((type, key) => {
        const known = panel.groups.find(g => g.key === key);
        overrides[known ? known.name : (panel.selected.get(key) || key)] = type;
    });
    return {
        groupFilterMode: panel.mode,
        groupFilterList: Array.from(panel.selected.values()),
        // not trimmed: whitespace is part of a regular expression
        groupFilterRegex: document.getElementById('source-group-filter-regex').value,
        importTypes: panel.types.size === FILTER_TYPES.length ? [] : FILTER_TYPES.filter(t => panel.types.has(t)),
        groupTypeOverrides: overrides,
    };
}

/**
 * Flags a pattern the browser cannot compile. The server has the final say
 * (it uses RE2), so Go-only syntax such as a leading (?i) is tolerated here.
 * @param {HTMLInputElement} input
 * @returns {boolean} whether the pattern looks valid
 */
function validateRegexInput(input) {
    const value = input.value.trim().replace(/^\(\?[ims]+\)/, '');
    let ok = true;
    try { if (value) new RegExp(value); } catch (e) { ok = false; }
    input.classList.toggle('invalid', !ok);
    input.title = ok ? '' : 'This pattern does not compile';
    return ok;
}

/**
 * Runs the preview shortly after a change, once a preview has been fetched.
 * The raw catalog is cached server-side, so re-evaluation is quick.
 */
function scheduleFilterPreview() {
    if (!filterPanel || !filterPanel.report) return;
    clearTimeout(filterPreviewTimer);
    filterPreviewTimer = setTimeout(() => runFilterPreview(false), 700);
}

/**
 * Tears the panel down when the source modal closes, so a pending debounce
 * cannot fetch for a modal that is gone.
 */
function closeFilterPanel() {
    clearTimeout(filterPreviewTimer);
    filterPreviewTimer = null;
    filterPanel = null;
}

/**
 * Evaluates the draft rules against the provider's catalog.
 * @param {boolean} force - re-download the catalog instead of using the cache
 */
async function runFilterPreview(force) {
    if (!filterPanel) return;
    if (filterPanel.loading) {
        // re-evaluate once the running preview lands, otherwise the result
        // shown would describe rules the controls no longer say
        filterPanel.pending = true;
        return;
    }
    const draft = readSourceForm();
    if (!draft.url) {
        showNotification('Enter the source URL first', 'warning');
        return;
    }
    if (FILTER_REGEX_IDS.some(id => !validateRegexInput(document.getElementById(id)))) {
        showNotification('Fix the highlighted pattern first', 'warning');
        return;
    }

    const panel = filterPanel;
    panel.loading = true;
    panel.pending = false;
    renderFilterSummary();
    try {
        const result = await apiCall('/api/sources/preview', {
            method: 'POST',
            body: JSON.stringify({ source: draft, force }),
            quiet: true,
        });
        if (panel !== filterPanel) return;
        panel.report = { ...result.report, cached: result.cached, durationMs: result.durationMs };
        setFilterGroups(result.report.groups || []);
        sourceRuleEditor.setStats(result.report.rules || []);
        panel.inventoryAt = null;
    } catch (error) {
        if (panel === filterPanel) showNotification('Preview failed: ' + error.message, 'danger');
    } finally {
        if (panel === filterPanel) {
            panel.loading = false;
            renderFilterPanel();
            if (panel.pending) {
                panel.pending = false;
                runFilterPreview(false);
            }
        }
    }
}

/**
 * Groups matching the search box and the type chip, in inventory order.
 * @returns {Array<Object>}
 */
function visibleFilterGroups() {
    // the chip compares against the type the row displays, override included.
    // the search matches the label literally, spacing and all: providers mark
    // whole families of groups by their prefix (a double space, an emoji), and
    // pairing such a search with "select visible" is the fast way to pick them.
    const search = filterPanel.search;
    return filterPanel.groups.filter(g => {
        const type = filterPanel.overrides.get(g.key) || g.type;
        if (filterPanel.typeChip && type !== filterPanel.typeChip) return false;
        if (!search) return true;
        return g.name.toLowerCase().includes(search) ||
            g.samples.some(s => s.toLowerCase().includes(search));
    });
}

/** Re-renders every part of the panel from state. */
function renderFilterPanel() {
    if (!filterPanel) return;
    renderFilterTypes();
    renderFilterMode();
    renderFilterGroups();
    renderFilterSummary();
    renderFilterResult();
}

/** Content type toggles with the catalog count of each type. */
function renderFilterTypes() {
    document.querySelectorAll('#filter-type-toggles .type-toggle').forEach(btn => {
        const type = btn.dataset.type;
        const on = filterPanel.types.has(type);
        btn.classList.toggle('on', on);
        btn.setAttribute('aria-pressed', on ? 'true' : 'false');
        const stat = filterPanel.report && filterPanel.report.byType && filterPanel.report.byType[type];
        let count = '';
        if (stat) count = `${formatCount(stat.kept)} / ${formatCount(stat.total)}`;
        else if (filterPanel.groups.length) count = formatCount(filterPanel.groups.filter(g => g.type === type).reduce((n, g) => n + g.total, 0));
        btn.querySelector('.type-count').textContent = count;
    });
}

/** Segmented mode control and its explanation. */
function renderFilterMode() {
    document.querySelectorAll('#filter-group-mode .seg-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.mode === filterPanel.mode);
    });
    const hints = {
        '': 'Every group is imported. Tick a group to keep only what you choose.',
        include: 'Only the ticked groups, plus any group matching the pattern, are imported.',
        exclude: 'Everything except the ticked groups, and any group matching the pattern, is imported.',
    };
    document.getElementById('filter-group-mode-hint').textContent = hints[filterPanel.mode] || hints[''];
}

/** The group list with selection, detected type, override and counts. */
function renderFilterGroups() {
    const list = document.getElementById('filter-group-list');
    const visible = visibleFilterGroups();
    const knownKeys = new Set(filterPanel.groups.map(g => g.key));
    const missing = Array.from(filterPanel.selected.entries()).filter(([key]) => !knownKeys.has(key));

    document.querySelectorAll('#filter-group-type-chips .chip').forEach(chip => {
        chip.classList.toggle('active', chip.dataset.type === filterPanel.typeChip);
    });

    if (!filterPanel.groups.length && !missing.length) {
        list.innerHTML = `<div class="px-3 py-6 text-center text-sm text-gray-400">
            No groups yet. Click <span class="text-gray-200">Preview</span> to fetch this source's catalog.</div>`;
    } else {
        const rows = [];
        missing.forEach(([key, name]) => rows.push(renderFilterGroupRow({ key, name, type: '', total: 0, kept: 0, samples: [] }, true)));
        visible.forEach(g => rows.push(renderFilterGroupRow(g, false)));
        if (!rows.length) {
            rows.push('<div class="px-3 py-6 text-center text-sm text-gray-400">No group matches the search</div>');
        }
        list.innerHTML = rows.join('');
    }

    const totalStreams = filterPanel.groups.reduce((n, g) => n + g.total, 0);
    document.getElementById('filter-group-footer').textContent = filterPanel.groups.length
        ? `${formatCount(visible.length)} of ${formatCount(filterPanel.groups.length)} groups · ${formatCount(totalStreams)} streams`
        : 'No groups loaded';

    const selectedStreams = filterPanel.groups.filter(g => filterPanel.selected.has(g.key)).reduce((n, g) => n + g.total, 0);
    const selectedEl = document.getElementById('filter-group-selected');
    selectedEl.textContent = filterPanel.selected.size
        ? `${formatCount(filterPanel.selected.size)} selected · ${formatCount(selectedStreams)} streams`
        : 'Nothing selected';
    selectedEl.classList.toggle('text-kptv-blue', filterPanel.selected.size > 0);
}

/**
 * One row of the group list.
 * @param {Object} g
 * @param {boolean} missing - selected earlier but absent from the current catalog
 * @returns {string}
 */
function renderFilterGroupRow(g, missing) {
    const selected = filterPanel.selected.has(g.key);
    const override = filterPanel.overrides.get(g.key) || '';
    const detected = FILTER_TYPE_LABEL[g.type] || 'Live';
    const label = g.name ? escapeHtml(g.name) : '<span class="italic text-gray-400">(no group)</span>';
    const sub = missing
        ? '<span class="text-orange-400">Not in the current catalog</span>'
        : escapeHtml(g.samples.join(' · '));
    const options = ['', ...FILTER_TYPES].map(t => {
        const text = t ? `${FILTER_TYPE_LABEL[t]}` : `Auto (${detected})`;
        return `<option value="${t}" ${t === override ? 'selected' : ''}>${text}</option>`;
    }).join('');
    const dot = g.type ? `<span class="type-dot ${FILTER_TYPE_DOT[override || g.type]}" title="${escapeAttr(override ? 'Forced ' + FILTER_TYPE_LABEL[override] : 'Detected ' + detected)}"></span>` : '<span class="type-dot bg-gray-600"></span>';
    const counts = (filterPanel.report || filterPanel.inventoryAt)
        ? `<span class="${g.kept ? 'text-gray-200' : 'text-gray-500'}">${formatCount(g.kept)}</span><span class="text-gray-500"> / ${formatCount(g.total)}</span>`
        : `<span class="text-gray-400">${formatCount(g.total)}</span>`;

    return `
        <div class="group-row ${selected ? 'selected' : ''} ${missing ? 'missing' : ''}" data-key="${escapeAttr(g.key)}">
            <input type="checkbox" class="group-check accent-kptv-blue flex-shrink-0" data-key="${escapeAttr(g.key)}" ${selected ? 'checked' : ''}>
            ${dot}
            <span class="flex-1 min-w-[10rem]">
                <span class="block text-sm truncate" title="${escapeAttr(g.name)}">${label}</span>
                <span class="block text-xs text-gray-500 truncate">${sub}</span>
            </span>
            <span class="ml-auto flex items-center gap-2 flex-shrink-0">
                <span class="flex gap-1">
                    <button type="button" class="group-rule chip" data-action="include" title="Add a rule keeping this group">+ Keep</button>
                    <button type="button" class="group-rule chip" data-action="exclude" title="Add a rule dropping this group">+ Drop</button>
                </span>
                <select class="group-type text-xs bg-kptv-gray border ${override ? 'border-kptv-blue text-kptv-blue' : 'border-kptv-border text-gray-300'} rounded px-1 py-0.5"
                    data-key="${escapeAttr(g.key)}" title="Content type for this group" ${missing ? 'disabled' : ''}>${options}</select>
                <span class="text-xs tabular-nums w-24 text-right">${counts}</span>
            </span>
        </div>`;
}

/** Summary bar and the preview buttons. */
function renderFilterSummary() {
    const main = document.getElementById('filter-summary-main');
    const sub = document.getElementById('filter-summary-sub');
    const btn = document.getElementById('filter-preview-btn');
    const spinner = document.getElementById('filter-preview-spinner');
    const refetch = document.getElementById('filter-refetch-btn');
    const report = filterPanel.report;

    btn.disabled = filterPanel.loading;
    refetch.disabled = filterPanel.loading;
    spinner.hidden = !filterPanel.loading;
    document.getElementById('filter-preview-label').textContent = filterPanel.loading
        ? 'Fetching…' : (report ? 'Re-evaluate' : 'Preview');

    if (filterPanel.loading) {
        main.textContent = report ? 'Re-evaluating the rules…' : 'Downloading the catalog from the provider…';
        sub.textContent = 'Large playlists can take a while; the result is cached for the next preview.';
        return;
    }
    if (report) {
        main.innerHTML = `<span class="text-kptv-blue font-semibold">${formatCount(report.kept)}</span> of ${formatCount(report.total)} streams will be imported`;
        const t = report.byType || {};
        const part = type => `${FILTER_TYPE_LABEL[type]} ${formatCount((t[type] || {}).kept || 0)}/${formatCount((t[type] || {}).total || 0)}`;
        const secs = (report.durationMs / 1000).toFixed(report.durationMs < 10000 ? 1 : 0);
        const unmatched = (report.rules || []).length
            ? ` · ${formatCount(report.defaultDecided)} matched no rule and were ${report.defaultAction === 'drop' ? 'dropped' : 'kept'}`
            : '';
        sub.textContent = `${FILTER_TYPES.map(part).join(' · ')}${unmatched} · ${report.cached ? 'evaluated from cache' : 'downloaded'} in ${secs}s`;
        return;
    }
    if (filterPanel.groups.length) {
        const total = filterPanel.groups.reduce((n, g) => n + g.total, 0);
        const kept = filterPanel.groups.reduce((n, g) => n + g.kept, 0);
        main.textContent = `Last import kept ${formatCount(kept)} of ${formatCount(total)} streams in ${formatCount(filterPanel.groups.length)} groups`;
        sub.textContent = (filterPanel.inventoryAt ? `Recorded ${timeAgo(filterPanel.inventoryAt)}. ` : '') + 'Preview evaluates the rules as they are now.';
        return;
    }
    main.textContent = 'No catalog loaded yet';
    sub.textContent = 'Preview downloads the playlist, lists its groups and shows what the rules keep.';
}

/** The result block: kept-stream samples from the last preview. */
function renderFilterResult() {
    const section = document.getElementById('filter-result');
    const report = filterPanel.report;
    section.hidden = !report;
    if (!report) return;

    const list = document.getElementById('filter-result-samples');
    const samples = report.keptSamples || [];
    list.innerHTML = samples.length
        ? samples.map(n => `<span class="px-2 py-0.5 rounded bg-kptv-gray border border-kptv-border text-xs">${escapeHtml(n)}</span>`).join('')
        : '<span class="text-sm text-orange-300">Nothing survives these rules.</span>';
    document.getElementById('filter-result-note').textContent = samples.length < report.kept
        ? `Showing the first ${samples.length} of ${formatCount(report.kept)} kept streams`
        : `All ${formatCount(report.kept)} kept streams`;
}
