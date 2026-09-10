/**
 * Fetches all configured sources and their import status from the API and
 * renders them into the sources container.
 * @returns {Promise<void>}
 */
async function loadSources() {
    try {
        const [config, status] = await Promise.all([
            apiCall('/api/config'),
            apiCall('/api/import/status', { quiet: true }).catch(() => null),
        ]);
        renderSources(config.sources || [], status);
        if (status) renderImportStatus(status);
    } catch (error) {
        document.getElementById('sources-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load sources</div>';
    }
}

/**
 * Renders source cards into the sources container, showing connection
 * settings, active filters, the last import outcome and action buttons.
 * @param {Array<Object>} sources - Source config objects from API
 * @param {Object|null} status - Import status from /api/import/status
 */
function renderSources(sources, status) {
    const container = document.getElementById('sources-container');

    if (sources.length === 0) {
        container.innerHTML = '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">No sources configured</div>';
        return;
    }

    const statusByUrl = {};
    ((status && status.sources) || []).forEach(s => { statusByUrl[s.url] = s; });

    container.innerHTML = sources.map((source, index) => `
        <div class="source-item" data-source-url="${escapeAttr(source.url)}">
            <div class="flex justify-between items-center mb-3">
                <div class="flex-1 min-w-0">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(source.name)}</h4>
                    <div class="text-gray-400 text-sm">${obfuscateUrl(source.url)}</div>
                </div>
                <div class="flex items-center flex-shrink-0">
                    <span class="status-indicator status-active"></span>
                    <span class="text-sm text-gray-400">Order: ${source.order}</span>
                </div>
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-3">
                <div class="text-sm">
                    <div class="text-gray-400">Max Connections</div>
                    <div>${source.maxConnections}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Timeout</div>
                    <div>${source.maxStreamTimeout}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Max Retries</div>
                    <div>${source.maxRetries}</div>
                </div>
                <div class="text-sm">
                    <div class="text-gray-400">Min Data Size</div>
                    <div>${source.minDataSize} KB</div>
                </div>
            </div>
            <div class="mb-2">
                <div class="text-sm text-gray-400 mb-1">Filters</div>
                <div class="flex flex-wrap gap-1">${renderFilterBadges(source)}</div>
            </div>
            ${source.userAgent ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">User Agent</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.userAgent)}</div>
                </div>
            ` : ''}
            ${source.reqOrigin ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">Origin</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.reqOrigin)}</div>
                </div>
            ` : ''}
            ${source.reqReferrer ? `
                <div class="mb-2">
                    <div class="text-sm text-gray-400">Referrer</div>
                    <div class="text-sm text-truncate">${escapeHtml(source.reqReferrer)}</div>
                </div>
            ` : ''}
            <div class="import-status text-xs text-gray-400 mt-3 flex items-center gap-2">${renderImportStatusLine(statusByUrl[source.url])}</div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex flex-wrap gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="editSource(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"></path>
                    </svg>
                    <span>Edit</span>
                </button>
                <button class="px-3 py-1 bg-green-700 hover:bg-green-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="importSource(${index})" title="Re-download this source and apply its filters">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
                    </svg>
                    <span>Import</span>
                </button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors flex items-center space-x-1"
                    onclick="deleteSource(${index})">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"></path>
                    </svg>
                    <span>Delete</span>
                </button>
            </div>
        </div>
    `).join('');
}

/**
 * Summarises a source's active filters as badges for its card.
 * @param {Object} source
 * @returns {string} HTML
 */
function renderFilterBadges(source) {
    const badge = (text, cls = 'bg-kptv-gray-light border border-kptv-border text-gray-300') =>
        `<span class="px-2 py-0.5 rounded text-xs ${cls}">${text}</span>`;
    const badges = [];

    const types = source.importTypes || [];
    if (types.length && types.length < FILTER_TYPES.length) {
        const cls = { live: 'bg-green-700 text-white', vod: 'bg-orange-700 text-white', series: 'bg-kptv-blue text-white' };
        types.forEach(t => badges.push(badge(`${FILTER_TYPE_LABEL[t] || t} only`, cls[t])));
    }

    const groups = (source.groupFilterList || []).length;
    const pattern = source.groupFilterRegex ? ' + pattern' : '';
    if (source.groupFilterMode === 'include') {
        badges.push(badge(`${formatCount(groups)} group${groups === 1 ? '' : 's'}${pattern}`));
    } else if (source.groupFilterMode === 'exclude') {
        badges.push(badge(`Excludes ${formatCount(groups)} group${groups === 1 ? '' : 's'}${pattern}`));
    }

    const patterns = ['liveIncludeRegex', 'liveExcludeRegex', 'seriesIncludeRegex', 'seriesExcludeRegex',
        'vodIncludeRegex', 'vodExcludeRegex', 'liveCategoryRegex', 'vodCategoryRegex', 'seriesCategoryRegex']
        .filter(k => source[k]).length;
    if (patterns) badges.push(badge(`${patterns} name pattern${patterns === 1 ? '' : 's'}`));

    const overrides = Object.keys(source.groupTypeOverrides || {}).length;
    if (overrides) badges.push(badge(`${overrides} type override${overrides === 1 ? '' : 's'}`));

    return badges.length ? badges.join('') : '<span class="text-xs text-gray-500">Everything is imported</span>';
}

/**
 * Renders one source's import status line.
 * @param {Object|undefined} st - Entry from /api/import/status
 * @returns {string} HTML
 */
function renderImportStatusLine(st) {
    if (st && st.running) {
        return '<span class="spinner-sm"></span><span>Importing…</span>';
    }
    if (!st || !st.lastImportAt) {
        return '<span>Not imported yet</span>';
    }
    if (!st.ok) {
        return `<span class="text-red-400">Import failed ${timeAgo(st.lastImportAt)}${st.error ? ': ' + escapeHtml(st.error) : ''}</span>`;
    }
    return `<span>Imported <span class="text-gray-200">${formatCount(st.kept)}</span> of ${formatCount(st.total)} streams · ${timeAgo(st.lastImportAt)} · ${(st.durationMs / 1000).toFixed(1)}s</span>`;
}

let importStatusTimer = null;
let importWasRunning = false;
let importRetrigger = false;

/**
 * Polls the import status while an import runs, updating the source cards,
 * and reloads the catalog views when it finishes.
 * @returns {Promise<void>}
 */
async function pollImportStatus() {
    clearTimeout(importStatusTimer);
    let status;
    try {
        status = await apiCall('/api/import/status', { quiet: true });
    } catch (error) {
        return;
    }
    renderImportStatus(status);

    if (status.running) {
        importWasRunning = true;
        importStatusTimer = setTimeout(pollImportStatus, 2000);
        return;
    }
    if (importWasRunning) {
        importWasRunning = false;
        showNotification('Import finished', 'success');
        loadSources();
        loadStats();
        loadAllChannels();
    }
    if (importRetrigger) {
        importRetrigger = false;
        triggerImport();
    }
}

/**
 * Updates the import banner and each source card's status line.
 * @param {Object} status
 */
function renderImportStatus(status) {
    const banner = document.getElementById('import-banner');
    const importAll = document.getElementById('import-all-btn');
    if (banner) banner.hidden = !status.running;
    if (importAll) importAll.disabled = status.running;
    if (status.running) {
        const running = (status.sources || []).filter(s => s.running).map(s => s.name);
        document.getElementById('import-banner-text').textContent = running.length
            ? `Importing ${running.join(', ')}…` : 'Importing…';
    }
    (status.sources || []).forEach(s => {
        const card = document.querySelector(`.source-item[data-source-url="${cssEscape(s.url)}"] .import-status`);
        if (card) card.innerHTML = renderImportStatusLine(s);
    });
}

/**
 * Asks the server to import now, applying the saved filters without a restart.
 * When an import is already running the request is repeated once it ends, so
 * settings saved mid-import still take effect.
 * @param {string} url - Source to re-download, or '' for every source
 * @param {boolean} force - Bypass the raw catalog cache
 * @returns {Promise<void>}
 */
async function triggerImport(url = '', force = false) {
    try {
        await apiCall('/api/import', { method: 'POST', body: JSON.stringify({ url, force }), quiet: true });
        showNotification(url ? 'Import started for this source' : 'Import started', 'primary');
    } catch (error) {
        if (/HTTP 409/.test(error.message)) {
            importRetrigger = true;
            showNotification('An import is already running; the new settings apply when it finishes', 'warning');
        } else {
            showNotification('Failed to start import: ' + error.message, 'danger');
            return;
        }
    }
    pollImportStatus();
}

/**
 * Re-downloads one source and re-applies its filters.
 * @param {number} index - Zero-based index of the source
 */
function importSource(index) {
    const source = adminConfig && adminConfig.sources && adminConfig.sources[index];
    if (!source) {
        showNotification('Config not loaded yet', 'warning');
        return;
    }
    triggerImport(source.url, true);
}

/**
 * Opens the source modal for adding a new source,
 * clearing the form and setting the modal title.
 */
function showSourceModal(sourceIndex = null) {
    const title = document.getElementById('source-modal-title');

    if (sourceIndex !== null) {
        title.textContent = 'Edit Source';
        if (adminConfig && adminConfig.sources && adminConfig.sources[sourceIndex]) {
            populateSourceForm(adminConfig.sources[sourceIndex], sourceIndex);
        } else {
            showNotification('Config not loaded yet', 'warning');
            loadGlobalSettings();
        }
    } else {
        title.textContent = 'Add Source';
        clearSourceForm();
    }

    // always open on the first tab; the filter tab is where the user last was
    const firstTab = document.querySelector('.source-tab-btn[data-tab="0"]');
    if (firstTab) firstTab.click();
    showModal('source-modal');
}

/**
 * Populates the source modal form fields with values from an existing source config.
 * @param {Object} source - Source config object to populate from
 * @param {number} index - Index of the source in the config array
 */
function populateSourceForm(source, index) {
    document.getElementById('source-index').value = index;
    document.getElementById('source-name').value = source.name || '';
    document.getElementById('source-url').value = source.url || '';
    document.getElementById('source-username').value = source.username || '';
    document.getElementById('source-password').value = source.password || '';
    document.getElementById('source-order').value = source.order || 1;
    document.getElementById('source-max-connections').value = source.maxConnections || 5;
    document.getElementById('source-max-stream-timeout').value = source.maxStreamTimeout || '10s';
    document.getElementById('source-retry-delay').value = source.retryDelay || '5s';
    document.getElementById('source-max-retries').value = source.maxRetries || 3;
    document.getElementById('source-max-failures').value = source.maxFailuresBeforeBlock || 5;
    document.getElementById('source-min-data-size').value = source.minDataSize || 2;
    document.getElementById('source-user-agent').value = source.userAgent || '';
    document.getElementById('source-origin').value = source.reqOrigin || '';
    document.getElementById('source-referrer').value = source.reqReferrer || '';
    document.getElementById('source-live-include-regex').value = source.liveIncludeRegex || '';
    document.getElementById('source-live-exclude-regex').value = source.liveExcludeRegex || '';
    document.getElementById('source-series-include-regex').value = source.seriesIncludeRegex || '';
    document.getElementById('source-series-exclude-regex').value = source.seriesExcludeRegex || '';
    document.getElementById('source-vod-include-regex').value = source.vodIncludeRegex || '';
    document.getElementById('source-vod-exclude-regex').value = source.vodExcludeRegex || '';
    document.getElementById('source-live-category-regex').value = source.liveCategoryRegex || '';
    document.getElementById('source-vod-category-regex').value = source.vodCategoryRegex || '';
    document.getElementById('source-series-category-regex').value = source.seriesCategoryRegex || '';
    resetFilterPanel(source);
}

/**
 * Resets all source modal form fields to their default values.
 */
function clearSourceForm() {
    document.getElementById('source-index').value = '';
    document.getElementById('source-form').reset();
    document.getElementById('source-username').value = '';
    document.getElementById('source-password').value = '';
    document.getElementById('source-order').value = 1;
    document.getElementById('source-max-connections').value = 5;
    document.getElementById('source-max-stream-timeout').value = '10s';
    document.getElementById('source-retry-delay').value = '5s';
    document.getElementById('source-max-retries').value = 3;
    document.getElementById('source-max-failures').value = 5;
    document.getElementById('source-min-data-size').value = 2;
    document.getElementById('source-live-include-regex').value = '';
    document.getElementById('source-live-exclude-regex').value = '';
    document.getElementById('source-series-include-regex').value = '';
    document.getElementById('source-series-exclude-regex').value = '';
    document.getElementById('source-vod-include-regex').value = '';
    document.getElementById('source-vod-exclude-regex').value = '';
    document.getElementById('source-live-category-regex').value = '';
    document.getElementById('source-vod-category-regex').value = '';
    document.getElementById('source-series-category-regex').value = '';
    resetFilterPanel(null);
}

/**
 * Reads the source modal form into a source object with the API's JSON names.
 * Shared by save and by the filter preview, so both see the same draft.
 * @returns {Object}
 */
function readSourceForm() {
    return {
        name: document.getElementById('source-name').value.trim(),
        url: document.getElementById('source-url').value.trim(),
        username: document.getElementById('source-username').value || '',
        password: document.getElementById('source-password').value || '',
        order: parseInt(document.getElementById('source-order').value) || 1,
        maxConnections: parseInt(document.getElementById('source-max-connections').value) || 5,
        maxStreamTimeout: document.getElementById('source-max-stream-timeout').value || '30s',
        retryDelay: document.getElementById('source-retry-delay').value || '5s',
        maxRetries: parseInt(document.getElementById('source-max-retries').value) || 3,
        maxFailuresBeforeBlock: parseInt(document.getElementById('source-max-failures').value) || 5,
        minDataSize: parseInt(document.getElementById('source-min-data-size').value) || 2,
        userAgent: document.getElementById('source-user-agent').value || '',
        reqOrigin: document.getElementById('source-origin').value || '',
        reqReferrer: document.getElementById('source-referrer').value || '',
        liveIncludeRegex: document.getElementById('source-live-include-regex').value.trim(),
        liveExcludeRegex: document.getElementById('source-live-exclude-regex').value.trim(),
        seriesIncludeRegex: document.getElementById('source-series-include-regex').value.trim(),
        seriesExcludeRegex: document.getElementById('source-series-exclude-regex').value.trim(),
        vodIncludeRegex: document.getElementById('source-vod-include-regex').value.trim(),
        vodExcludeRegex: document.getElementById('source-vod-exclude-regex').value.trim(),
        liveCategoryRegex: document.getElementById('source-live-category-regex').value.trim(),
        vodCategoryRegex: document.getElementById('source-vod-category-regex').value.trim(),
        seriesCategoryRegex: document.getElementById('source-series-category-regex').value.trim(),
        ...collectFilterFields(),
    };
}

/**
 * Reads the source modal form, validates it, saves the source to the config
 * via the API and starts an import so the change applies without a restart.
 * @returns {Promise<void>}
 */
async function saveSource() {
    try {
        const index = document.getElementById('source-index').value;
        const source = readSourceForm();

        if (!source.name || !source.url) {
            showNotification('Name and URL are required', 'danger');
            return;
        }
        if (source.groupFilterMode === 'include' && !source.groupFilterList.length && !source.groupFilterRegex) {
            showNotification('"Only selected groups" needs at least one group or a group pattern', 'danger');
            return;
        }
        if (FILTER_REGEX_IDS.some(id => !validateRegexInput(document.getElementById(id)))) {
            showNotification('Fix the highlighted pattern first', 'danger');
            return;
        }

        const config = await apiCall('/api/config');
        if (!config.sources) config.sources = [];
        if (index === '') {
            config.sources.push(source);
        } else {
            config.sources[parseInt(index)] = source;
        }
        delete config.xcOutputAccounts;
        delete config.epgs;
        delete config.sdAccounts;
        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config), quiet: true });

        hideModal('source-modal');
        showNotification('Source saved', 'success');
        loadGlobalSettings();
        loadSources();
        triggerImport(source.url);
    } catch (error) {
        showNotification('Failed to save source: ' + error.message, 'danger');
    }
}

/**
 * Opens the source modal pre-populated with the source at the given index.
 * @param {number} index - Zero-based index of the source to edit
 */
function editSource(index) {
    showSourceModal(index);
}

/**
 * Deletes the source at the given index from config after user confirmation,
 * then imports so its channels leave the catalog.
 * @param {number} index - Zero-based index of the source to delete
 * @returns {Promise<void>}
 */
async function deleteSource(index) {
    if (!confirm('Are you sure you want to delete this source?')) return;

    try {
        const config = await apiCall('/api/config');
        config.sources.splice(index, 1);
        delete config.xcOutputAccounts;
        delete config.epgs;
        delete config.sdAccounts;
        await apiCall('/api/config', { method: 'POST', body: JSON.stringify(config) });
        showNotification('Source deleted successfully!', 'success');
        loadGlobalSettings();
        loadSources();
        triggerImport();
    } catch (error) {
        showNotification('Failed to delete source', 'danger');
    }
}
