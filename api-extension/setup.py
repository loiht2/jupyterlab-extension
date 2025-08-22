from setuptools import setup

setup(
    name="api_extension",
    description="A JupyterLab server extension to expose configuration through API",
    author={"name": "Thanh-Loi Hoang", "email": "loi.hoangthanh.24@gmail.com"},
    version="0.0.1",
    packages=["api_extension"],
    include_package_data=True,
    data_files=[
        (
            "etc/jupyter/jupyter_server_config.d/",
            ["jupyter-config/jupyter_server_config.d/api_extension.json"],
        ),
    ],
)