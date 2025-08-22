import { JupyterFrontEnd, JupyterFrontEndPlugin } from '@jupyterlab/application';
import { NotebookActions } from '@jupyterlab/notebook';
import { showDialog, Dialog } from '@jupyterlab/apputils';
import { Widget } from '@lumino/widgets';
import { PageConfig, URLExt } from '@jupyterlab/coreutils';

// ---------- Config & constants ----------
const IDLE_MINUTES = 3;
const IDLE_MS = IDLE_MINUTES * 60 * 1000;

// Regex to detect GPU requirements (customize if needed)
const GPU_PATTERNS: ReadonlyArray<RegExp> = Object.freeze([
  /\btorch\.cuda\(/i,
  /\btorch\.device\(['"]cuda['"]\)/i,
  /\btorch\.to\(['"]cuda['"]\)/i,
  /\bmodel\.to\(['"]cuda['"]\)/i,
  /\bcupy\./i,
  /\bcp\.array\b/i,
  /tensorflow\.config\.list_physical_devices\(['"]GPU['"]\)/i,
  /\btf\.device\(['"]\/?GPU/i,
  /\.cuda\(/i, // tensor.cuda()
  /\bjax\.devices\(.+GPU/i,
  /\bjax\.default_backend\(.+gpu/i
]);

function detectGPU(code: string): boolean {
  if (!code) return false;
  return GPU_PATTERNS.some(rx => rx.test(code));
}

// Minimal cell source getter compatible across JL3/4
function getCellSource(cell: any): string {
  return (
    cell?.model?.value?.text ??
    cell?.model?.sharedModel?.getSource?.() ??
    ''
  );
}

// ---------- Runtime config ----------
interface RuntimeConfig {
  usingGPU?: boolean | 'true' | 'false' | 'unknown';
  PodName?: string;
  PodNamespace?: string;
}

let state = {
  usingGPU: false, // single source of truth
  lastGpuUseTime: 0,
  idleTimerId: null as number | null,
  inFlight: false,
  PodName: 'unknown',
  PodNamespace: 'unknown',
};


void (async () => {
  try {
    const baseUrl = PageConfig.getBaseUrl();
    const url = URLExt.join(baseUrl, 'api_extension');
    const res = await fetch(url, { cache: 'no-store', credentials: 'same-origin' });
    if (!res.ok) throw new Error(`HTTP ${res.status} @ ${res.url}`);
    const cfg: Partial<RuntimeConfig> = await res.json();

    const flag = cfg.usingGPU;
    state.usingGPU = flag === true || flag === 'true';
    state.PodName = cfg.PodName ?? state.PodName;
    state.PodNamespace = cfg.PodNamespace ?? state.PodNamespace;

    console.log('state is:', state.usingGPU, state.PodName, state.PodNamespace);
  } catch (e) {
    console.log('fetch failed', e);
  }
})();


// ---------- UI helpers ----------
function openWaitingDialog(title = 'Preparing notebook') {
  const bodyNode = document.createElement('div');
  bodyNode.style.display = 'grid';
  bodyNode.style.gap = '8px';
  // bodyNode.style.minWidth = '520px';
  bodyNode.style.minHeight = '140px'; 

  const status = document.createElement('div');
  status.textContent = 'Waiting for server to return a link…';

  const connect = document.createElement('button');
  connect.textContent = 'CONNECT';
  connect.disabled = true;
  Object.assign(connect.style, {
    opacity: '0.5', cursor: 'not-allowed', padding: '6px 12px',
    borderRadius: '8px', border: '1px solid var(--jp-border-color2, #ddd)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: '100%',   // if want the button to be full width
    height: '40px'   // set a fixed height for stable vertical alignment
  } as CSSStyleDeclaration);

  bodyNode.appendChild(status);
  bodyNode.appendChild(connect);

  const bodyWidget = new Widget({ node: bodyNode });

  const dlgPromise = showDialog({
    title,
    body: bodyWidget,
    buttons: [Dialog.cancelButton({ label: 'Close' })]
  });

  return { dlgPromise, status, connect };
}

// Accept both newURL or (namespace,name)
interface BackendResponse {
  status?: string;
  namespace?: string;
  name?: string;
  newURL?: string;
  [k: string]: unknown;
}

function enableConnect(connect: HTMLButtonElement, status: HTMLDivElement, data: BackendResponse) {
  status.textContent = 'Ready to connect.';
  connect.disabled = false;
  connect.style.opacity = '1';
  connect.style.cursor = 'pointer';
  connect.onclick = () => {
    if (typeof data.newURL === 'string' && data.newURL) {
      const url = data.newURL.startsWith('http')
        ? data.newURL
        : (data.newURL.startsWith('/') ? data.newURL : `/${data.newURL}`);
      window.open(url, '_blank');
    } else if (data.namespace && data.name) {
      const ns = String(data.namespace);
      const nm = String(data.name);
      window.open(`/notebook/${ns}/${nm}/`, '_blank');
    } else {
      void showDialog({
        title: 'Error',
        body: 'No notebook URL found in response.',
        buttons: [Dialog.okButton({ label: 'OK' })]
      });
    }
  };
}

const apiUrl = new URL('/jupyter/abe/messages', window.location.origin).toString();

async function waitAndConnect(title: string, payload: Record<string, unknown>) {
  if (state.inFlight) return; // simple guard to avoid duplicate requests
  state.inFlight = true;
  const { dlgPromise, status, connect } = openWaitingDialog(title);
  const ctrl = new AbortController();

  // If the user closes the dialog before the server responds, abort the request
  void dlgPromise.then(() => ctrl.abort('Dialog closed'));

  try {
    const res = await fetch(apiUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: ctrl.signal
    });

    if (!res.ok) {
      const txt = await res.text().catch(() => '');
      throw new Error(`HTTP ${res.status}: ${txt || res.statusText}`);
    }

    const data: BackendResponse = await res.json();
    enableConnect(connect, status as HTMLDivElement, data);
  } catch (err: any) {
    console.error('Error waiting for link:', err);
    (status as HTMLDivElement).textContent =
      /Abort/.test(String(err?.message ?? err))
        ? 'Cancelled.'
        : 'Request failed or connection interrupted.';
    connect.disabled = true;
    connect.style.opacity = '0.5';
    connect.style.cursor = 'not-allowed';
  } finally {
    state.inFlight = false;
  }
}

const KISHU_COMMIT_CMD = 'kishu:commit';
async function commitBeforeProceed(app: JupyterFrontEnd<JupyterFrontEnd.IShell, "desktop" | "mobile">) {
  if (app.commands.hasCommand(KISHU_COMMIT_CMD)) {
    try {
      // If the extension supports args, pass a message:
      await app.commands.execute(KISHU_COMMIT_CMD, {
        message: 'Auto-commit before GPU switch',
      });
    } catch (err) {
      console.warn('Kishu commit failed or was cancelled:', err);
    }
  } else {
    console.warn('Cannot find command kishu:commit; continue without commit.');
  }
}

function resetGpuIdleTimer(app: JupyterFrontEnd<JupyterFrontEnd.IShell, "desktop" | "mobile">) {
  if (state.idleTimerId !== null) {
    window.clearTimeout(state.idleTimerId);
  }
  state.idleTimerId = window.setTimeout(() => {
    // Prompt to switch back to CPU after IDLE_MINUTES of no GPU use
    showDialog({
      title: 'Switch back to CPU-only?',
      body: `No GPU activity detected for ${IDLE_MINUTES} minutes. Switch this notebook back to a CPU-only pod to save resources?`,
      buttons: [
        Dialog.cancelButton({ label: 'No' }),
        Dialog.okButton({ label: 'Yes' })
      ]
    }).then(async result => {
      if (result.button.accept) {
        // ← Commit Kishu before doing anything else
        try {
          await commitBeforeProceed(app); // kishu:commit
        } catch (e) {
          console.warn('Commit failed/cancelled, continue anyway:', e);
        }        
        console.log('Switching to CPU-only due to GPU idle.');
        const payload = {
          NotifyGPUReleased: 'true', // keep string if backend expects it
          PodName: state.PodName,
          PodNamespace: state.PodNamespace
        };
        void waitAndConnect('Switching to CPU-only notebook', payload);
        state.usingGPU = false; // optimistic
        state.idleTimerId = null;
      } else {
        // User keeps GPU → restart the timer window
        state.lastGpuUseTime = Date.now();
        resetGpuIdleTimer(app);
      }
    });
  }, IDLE_MS);
}

// ---------- Plugin ----------
const plugin: JupyterFrontEndPlugin<void> = {
  id: 'auto-cpu-gpu-switch:plugin',
  autoStart: true,
  activate: (app: JupyterFrontEnd) => {
    console.log('[Auto CPU/GPU Switcher] activated');
    
    state.lastGpuUseTime = Date.now();
    // BEFORE execution: detect if upcoming cell likely needs GPU while we are on CPU
    NotebookActions.executionScheduled.connect((_, args: any) => {
      const { cell } = args ?? {};
      if (!cell || cell.model?.type !== 'code') return;

      const needsGPU = detectGPU(getCellSource(cell));
      if (state.usingGPU) {
        resetGpuIdleTimer(app);
      }
      if (needsGPU){
        if (!state.usingGPU) {
          showDialog({
            title: 'This cell likely needs a GPU',
            body: 'Switch to a GPU notebook before running?',
            buttons: [Dialog.cancelButton({ label: 'No' }), Dialog.okButton({ label: 'Yes' })]
          }).then(async result => {
            if (result.button.accept) {
              // ← Commit Kishu before doing anything else
              try {
                await commitBeforeProceed(app); // kishu:commit
              } catch (e) {
                console.warn('Commit failed/cancelled, continue anyway:', e);
              } 
              console.log('Detected GPU requirement; requesting GPU environment …');
              console.log('State: ', state);
              const payload = {
                NotifyGPUNeeded: 'true',
                PodName: state.PodName,
                PodNamespace: state.PodNamespace
              };
              console.log('apiUrl:', apiUrl)
              void waitAndConnect('Switching to GPU notebook', payload);
          }
        });
      }
        if (state.usingGPU) {
          console.log('Already using GPU, no need to switch.');
          state.lastGpuUseTime = Date.now();
        }
    }
      if (!needsGPU) {
        console.log('No GPU needed for this cell.');
        if (state.usingGPU) {
          resetGpuIdleTimer(app);
        }
      }
    });

    // AFTER execution: if we are on GPU and the cell used GPU successfully, refresh the idle timer
    NotebookActions.executed.connect((_, args: any) => {
      const { cell, success } = args ?? {};
      if (!cell || cell.model?.type !== 'code') return;

      if (state.usingGPU) {
        const usedGPU = success && detectGPU(getCellSource(cell));
        if (usedGPU) {
          state.lastGpuUseTime = Date.now();
          console.log('[Auto CPU/GPU Switcher] GPU used at', new Date(state.lastGpuUseTime));
          resetGpuIdleTimer(app);
        }
      }
    });
  }
};

export default plugin;
