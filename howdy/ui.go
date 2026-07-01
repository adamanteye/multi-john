package howdy

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>John the Ripper</title>
  <style>
    :root {
      --bg: #fff;
      --text: #111;
      --link: #b91c1c;
      --link-hover: #7f1d1d;
      --border: #ccc;
      --code-bg: #f4f4f4;
      --code-border: #d7d7d7;
      --font-main: "Iosevka", "LXGW WenKai TC CJK", "LXGW WenKai SC CJK", monospace;
      --font-code: "Iosevka", "LXGW WenKai TC CJK", "LXGW WenKai SC CJK", monospace;
      --size-body: 14pt;
      --line-body: 1.55;
      --content-max: 80ch;
      --space-body: 1rem;
      --radius: 0.25rem;
    }
    *, *::before, *::after {
      box-sizing: border-box;
    }
    body {
      font-family: var(--font-main);
      font-size: var(--size-body);
      line-height: var(--line-body);
      display: grid;
      grid-template-rows: auto 1fr;
      min-height: 100vh;
      max-width: var(--content-max);
      width: 100%;
      margin: 0 auto;
      padding: var(--space-body);
      background: var(--bg);
      color: var(--text);
    }
    header {
      padding-bottom: 0.5em;
      box-shadow: 0 0.1em 0 var(--border);
      margin-bottom: 1em;
    }
    h1, h2 {
      line-height: 1.2;
      font-weight: 600;
      margin: 1.4em 0 0.6em;
    }
    h1 {
      font-size: 2em;
    }
    h2 {
      font-size: 1.45em;
    }
    p {
      margin: 0 0 0.8em;
    }
    label {
      display: block;
      margin: 0.9em 0;
      font-weight: 600;
    }
    label span {
      display: block;
      margin-bottom: 0.25em;
    }
    input, textarea, button {
      font: inherit;
    }
    input, textarea {
      width: 100%;
      padding: 0.35rem 0.45rem;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: var(--bg);
      color: var(--text);
      font-family: var(--font-code);
    }
    textarea {
      min-height: 12rem;
      resize: vertical;
    }
    textarea.job-yaml {
      min-height: 34rem;
    }
    button {
      padding: 0.35rem 0.7rem;
      border: 1px solid var(--link);
      border-radius: var(--radius);
      background: var(--bg);
      color: var(--link);
      cursor: pointer;
    }
    button:hover, button:focus {
      color: var(--link-hover);
      border-color: var(--link-hover);
    }
    button:disabled {
      opacity: 0.6;
      cursor: wait;
    }
    .grid {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 0.75rem;
    }
    .actions {
      display: flex;
      gap: 0.5rem;
      flex-wrap: wrap;
      align-items: center;
    }
    .status, small {
      color: #555;
    }
    .error {
      color: #b91c1c;
    }
    table {
      display: block;
      width: max-content;
      min-width: 100%;
      max-width: 100%;
      overflow-x: auto;
      border-collapse: collapse;
    }
    th, td {
      padding: 0.2rem 0.55rem;
      vertical-align: top;
    }
    th {
      text-align: left;
      border-top: 2px solid #000;
      border-bottom: 2px solid #000;
      white-space: nowrap;
    }
    tr:last-child td {
      border-bottom: 2px solid #000;
    }
    pre {
      margin: 1em 0;
      padding: 0.9rem 1rem;
      overflow-x: auto;
      background: var(--code-bg);
      border: 1px solid var(--code-border);
      border-radius: var(--radius);
      font-family: var(--font-code);
      white-space: pre-wrap;
    }
    @media (max-width: 600px) {
      body {
        padding: 0.75rem;
      }
      .grid {
        grid-template-columns: 1fr;
      }
    }
  </style>
</head>
<body>
  <header>
    <h1>John the Ripper</h1>
  </header>
  <main>
    <h2>Submit</h2>
    <form id="jobForm">
      <label><span>Job YAML</span><textarea id="jobYAML" class="job-yaml" required spellcheck="false"></textarea></label>
      <div class="actions">
        <button id="submit" type="submit">Create job</button>
        <button id="refresh" type="button">Refresh</button>
        <span class="status" id="formStatus"></span>
      </div>
    </form>

    <h2>Work</h2>
    <div id="work"><p class="status">Loading...</p></div>

    <h2>Jobs</h2>
    <div id="jobs"><p class="status">Loading...</p></div>

    <h2>Results</h2>
    <pre id="results">{}</pre>
  </main>
  <script>
    const jobsEl = document.getElementById('jobs');
    const workEl = document.getElementById('work');
    const resultsEl = document.getElementById('results');
    const statusEl = document.getElementById('formStatus');
    const submitEl = document.getElementById('submit');
    let selectedRunID = '';
    const resultCountCache = {};

    async function request(path, options) {
      const res = await fetch(path, options);
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || res.statusText);
      return data;
    }

    async function loadConfig() {
      const config = await request('/api/config');
      document.getElementById('jobYAML').value = config.defaultJobYAML || '';
    }

    async function loadJobs() {
      try {
        const jobs = await request('/api/jobs');
        if (!jobs.length) {
          jobsEl.innerHTML = '<p class="status">No jobs submitted.</p>';
          resultsEl.textContent = '{}';
          return;
        }
        jobs.sort((a, b) => String(b.createdAt).localeCompare(String(a.createdAt)));
        const rows = jobs.map((job) => {
          const progress = (job.succeeded || 0) + '/' + (job.completions || 0);
          const resultCount = Object.prototype.hasOwnProperty.call(resultCountCache, job.runID) ? resultCountCache[job.runID] : '';
          return '<tr><td>' + escapeHTML(job.runID) + '</td><td>' + progress + '</td><td>' + escapeHTML(resultCount) + '</td><td>' + (job.active || 0) + '</td><td>' + (job.failed || 0) + '</td><td><button type="button" data-run="' + escapeHTML(job.runID) + '">View</button></td></tr>';
        }).join('');
        jobsEl.innerHTML = '<table><thead><tr><th>Run</th><th>Done</th><th>Results</th><th>Active</th><th>Failed</th><th></th></tr></thead><tbody>' + rows + '</tbody></table>';
        for (const button of jobsEl.querySelectorAll('button[data-run]')) {
          button.addEventListener('click', () => loadResults(button.dataset.run));
        }
        if (!selectedRunID && jobs[0] && jobs[0].runID) await loadResults(jobs[0].runID);
      } catch (err) {
        jobsEl.innerHTML = '<p class="error">' + escapeHTML(err.message) + '</p>';
      }
    }

    async function loadWork() {
      try {
        const listing = await request('/api/work');
        const entries = listing.entries || [];
        if (!entries.length) {
          workEl.innerHTML = '<p class="status">' + escapeHTML(listing.path || '/work') + ' is empty.</p>';
          return;
        }
        const rows = entries.map((entry) => {
          const name = entry.name + (entry.directory ? '/' : '');
          const size = entry.directory ? '' : formatBytes(entry.size || 0);
          const modifiedAt = entry.modifiedAt ? new Date(entry.modifiedAt).toLocaleString() : '';
          return '<tr><td>' + escapeHTML(name) + '</td><td>' + escapeHTML(size) + '</td><td>' + escapeHTML(modifiedAt) + '</td></tr>';
        }).join('');
        workEl.innerHTML = '<p class="status">' + escapeHTML(listing.path || '/work') + '</p><table><thead><tr><th>Name</th><th>Size</th><th>Modified</th></tr></thead><tbody>' + rows + '</tbody></table>';
      } catch (err) {
        workEl.innerHTML = '<p class="error">' + escapeHTML(err.message) + '</p>';
      }
    }

    async function loadResults(runID) {
      selectedRunID = runID;
      try {
        const results = await request('/api/results?runID=' + encodeURIComponent(runID));
        resultCountCache[runID] = countResults(results);
        resultsEl.textContent = JSON.stringify(results, null, 2);
        await loadJobs();
      } catch (err) {
        resultsEl.textContent = err.message;
      }
    }

    function countResults(results) {
      let count = 0;
      const nodes = results.node || {};
      for (const node of Object.values(nodes)) {
        if (Array.isArray(node.results)) count += node.results.length;
      }
      return count;
    }

    function escapeHTML(value) {
      return String(value).replace(/[&<>"']/g, (char) => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[char]));
    }

    function formatBytes(value) {
      if (value < 1024) return value + ' B';
      const units = ['KiB', 'MiB', 'GiB', 'TiB'];
      let size = value / 1024;
      let index = 0;
      while (size >= 1024 && index < units.length - 1) {
        size /= 1024;
        index++;
      }
      return size.toFixed(size >= 10 ? 1 : 2) + ' ' + units[index];
    }

    document.getElementById('jobForm').addEventListener('submit', async (event) => {
      event.preventDefault();
      submitEl.disabled = true;
      statusEl.className = 'status';
      statusEl.textContent = 'Creating...';
      try {
        const payload = {
          jobYAML: document.getElementById('jobYAML').value
        };
        const created = await request('/api/jobs', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(payload)
        });
        statusEl.textContent = 'Created ' + created.jobName;
        selectedRunID = created.runID;
        await loadJobs();
        await loadResults(created.runID);
      } catch (err) {
        statusEl.textContent = err.message;
        statusEl.className = 'status error';
      } finally {
        submitEl.disabled = false;
      }
    });

    document.getElementById('refresh').addEventListener('click', async () => {
      await loadWork();
      await loadJobs();
      if (selectedRunID) await loadResults(selectedRunID);
    });

    loadConfig().catch((err) => statusEl.textContent = err.message);
    loadWork();
    loadJobs();
    setInterval(async () => {
      await loadWork();
      await loadJobs();
      if (selectedRunID) await loadResults(selectedRunID);
    }, 5000);
  </script>
</body>
</html>`
