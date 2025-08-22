from jupyter_server.base.handlers import JupyterHandler
from jupyter_server.serverapp import ServerApp
import tornado
import json

# Path to the runtime configuration file in the system 
CONFIG_PATH = "/tmp/runtime-cfg/runtime-config.json"

class RuntimeConfigHandler(JupyterHandler):
    @tornado.web.authenticated
    def get(self):
        try:
            with open(CONFIG_PATH) as f:
                data = json.load(f)
        except FileNotFoundError:
            raise tornado.web.HTTPError(404, f"Not found: {CONFIG_PATH}")
        except Exception as e:
            raise tornado.web.HTTPError(500, f"Read error: {e}")
        self.finish(data)  # write JSON & end the response

def _load_jupyter_server_extension(serverapp: ServerApp):
    # Define URL pattern for endpoint
    base_url = serverapp.web_app.settings.get("base_url", "/")
    route_pattern = base_url + "api_extension" # Endpoint: /<base_url>/api_extension
    host_pattern = ".*$"   # Accept every host
    handlers = [(route_pattern, RuntimeConfigHandler)]
    serverapp.web_app.add_handlers(host_pattern, handlers) # Register handler for endpoint

def _jupyter_server_extension_points():
    return [{"module": "api_extension"}]
