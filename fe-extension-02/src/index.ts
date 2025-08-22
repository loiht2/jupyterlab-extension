import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin
} from '@jupyterlab/application';
import { PageConfig, URLExt } from '@jupyterlab/coreutils';
import { Widget } from '@lumino/widgets';


import {
  ICommandPalette,
  CommandToolbarButton,
  Dialog,
  showDialog
} from '@jupyterlab/apputils';

import { INotebookTracker } from '@jupyterlab/notebook';

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
// abe: another back-end 

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
      // If the extension supports args, you can pass message:
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

/**
 * Plugin switch button
 */
const plugin: JupyterFrontEndPlugin<void> = {
  id: 'switch-button:plugin',
  description: 'Thêm nút Kiểm tra GPU vào notebook toolbar',
  autoStart: true,
  requires: [INotebookTracker],
  optional: [ICommandPalette],
  activate: (
    app: JupyterFrontEnd,
    tracker: INotebookTracker,
    palette?: ICommandPalette
  ) => {
    console.log('Switch button plugin activated');
    const { commands } = app;
    const CMD_ID = 'switch-button:button';

    /* 1️⃣ Define a new command */
    commands.addCommand(CMD_ID, {
      label: 'Migrate',
      caption: 'Migrate notebook from CPU to GPU pod or vice versa',
      // Only open when a notebook is focused
      isEnabled: () => !!tracker.currentWidget,
      execute: async () => {
        const panel = tracker.currentWidget;
        if (!panel) {
          await showDialog({
            title: 'No active notebook',
            body: 'Let\'s open a notebook first.',
            buttons: [Dialog.okButton()]
          });
          return;
        }
        // Wait for readiness
        await panel.context.ready.catch(() => void 0);
        await panel.sessionContext.ready.catch(() => void 0);
        
      if (!state.usingGPU){
        showDialog({
          title: 'Do you want to move to GPU notebook?',
          body: 'Switch to a GPU notebook?',
          buttons: [Dialog.cancelButton({ label: 'No' }), Dialog.okButton({ label: 'Yes' })]
        }).then(async result => {
          if (result.button.accept) {
            // ← Commit Kishu before switching
            try {
              await commitBeforeProceed(app); // kishu:commit
            } catch (e) {
              console.warn('Commit failed/cancelled, continue anyway:', e);
            } 
            console.log('Migrate to GPU environment');
            const payload = {
              NotifyGPUNeeded: 'true',
              PodName: state.PodName,
              PodNamespace: state.PodNamespace
            };
            void waitAndConnect('Switching to GPU notebook', payload);
          }
        });
      }
      if (state.usingGPU){
        showDialog({
          title: 'Do you want to move to CPU-only notebook?',
          body: 'Switch to a CPU-only notebook?',
          buttons: [Dialog.cancelButton({ label: 'No' }), Dialog.okButton({ label: 'Yes' })]
        }).then(async result => {
          if (result.button.accept) {
            // ← Commit Kishu before switching
            try {
              await commitBeforeProceed(app); // kishu:commit
            } catch (e) {
              console.warn('Commit failed/cancelled, continue anyway:', e);
            } 
            console.log('Migrate back to CPU pod');
            const payload = {
              NotifyGPUReleased: 'true',
              PodName: state.PodName,
              PodNamespace: state.PodNamespace
            };
            void waitAndConnect('Switching to CPU notebook', payload);
          }
        });
      }
      }
    });

    /* 2️⃣ Add toolbar button when opening NotebookPanel */
    tracker.widgetAdded.connect((_, panel) => {
      panel.toolbar.insertItem(
        10,
        'switchButton',
        new CommandToolbarButton({ commands, id: CMD_ID })
      );
    });

    /* 3️⃣ (Optional) Add command to Command Palette */
    palette?.addItem({ command: CMD_ID, category: 'GPU' });
  }
};

export default plugin;
