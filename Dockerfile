#
# NOTE: Use the Makefiles to build this image correctly.
#

ARG BASE_IMG=registry.local:80/loiht2/nb-jupyter:v1.0
# You need to change this base image to yours. 
FROM $BASE_IMG

ARG TARGETARCH

# args - software versions
#  - TensorFlow CUDA version matrix: https://www.tensorflow.org/install/source#gpu
#  - Extra PyPi from NVIDIA (for TensorRT): https://pypi.nvidia.com/
#  - TODO: it seems like TensorRT will be removed from TensorFlow in 2.18.0
#          when updating past that version, remember to remove all TensorRT
#          related packages and configs in this Dockerfile
ARG TENSORFLOW_VERSION=2.15.1
ARG TENSORRT_VERSION=8.6.1.post1
ARG TENSORRT_LIBS_VERSION=8.6.1
ARG TENSORRT_BINDINGS_VERSION=8.6.1

# nvidia container toolkit
# https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/docker-specialized.html
ENV NVIDIA_VISIBLE_DEVICES all
ENV NVIDIA_DRIVER_CAPABILITIES compute,utility
ENV NVIDIA_REQUIRE_CUDA "cuda>=12.2"

# install - tensorflow
#  - About '[and-cuda]' option: https://github.com/tensorflow/tensorflow/blob/v2.15.1/tensorflow/tools/pip_package/setup.py#L166-L177
RUN python3 -m pip install --quiet --no-cache-dir --extra-index-url https://pypi.nvidia.com \
    tensorflow[and-cuda]==${TENSORFLOW_VERSION} \
    tensorrt==${TENSORRT_VERSION} \
    tensorrt-libs==${TENSORRT_LIBS_VERSION} \
    tensorrt-bindings==${TENSORRT_BINDINGS_VERSION}

# create symlinks for TensorRT libs
#  - https://github.com/tensorflow/tensorflow/issues/61986#issuecomment-1880489731
#  - libnvinfer.so.8.6.1 -> libnvinfer.so.8
#  - libnvinfer_plugin.so.8.6.1 -> libnvinfer_plugin.so.8
ENV PYTHON_SITE_PACKAGES /opt/conda/lib/python3.11/site-packages
ENV TENSORRT_LIBS ${PYTHON_SITE_PACKAGES}/tensorrt_libs
RUN ln -s ${TENSORRT_LIBS}/libnvinfer.so.${TENSORRT_LIBS_VERSION%%.*} ${TENSORRT_LIBS}/libnvinfer.so.${TENSORRT_LIBS_VERSION} \
 && ln -s ${TENSORRT_LIBS}/libnvinfer_plugin.so.${TENSORRT_LIBS_VERSION%%.*} ${TENSORRT_LIBS}/libnvinfer_plugin.so.${TENSORRT_LIBS_VERSION}

# envs - cudnn, tensorrt
ENV LD_LIBRARY_PATH ${LD_LIBRARY_PATH}/nvidia/cudnn/lib:${TENSORRT_LIBS}

# install - requirements.txt
COPY --chown=${NB_USER}:${NB_GID} requirements.txt /tmp
RUN python3 -m pip install -r /tmp/requirements.txt --quiet --no-cache-dir \
 && rm -f /tmp/requirements.txt

# Install jupyterlab_kishu extension
RUN python3 -m pip install --no-cache-dir jupyterlab_kishu

USER root
# Install fe-extension-01 (typescript)
COPY --chown=${NB_USER}:${NB_GID} fe-extension-01/ /opt/app/fe-extension-01/
WORKDIR /opt/app/fe-extension-01
RUN jlpm install && jlpm run build && jupyter labextension develop . --overwrite

# Install fe-extension-02 (typescript)
COPY --chown=${NB_USER}:${NB_GID} fe-extension-02/ /opt/app/fe-extension-02/
WORKDIR /opt/app/fe-extension-02
RUN jlpm install && jlpm run build && jupyter labextension develop . --overwrite

# Install api-extension (python)
COPY --chown=${NB_USER}:${NB_GID} api-extension/ /opt/app/api-extension/
WORKDIR /opt/app/api-extension
RUN python3 -m pip install --no-cache-dir .
RUN jupyter server extension enable --py api_extension --sys-prefix

# Copy entrypoint.sh and grant execution permission
COPY --chown=${NB_USER}:${NB_GID} entrypoint.sh /usr/local/bin/runtime-config.sh
RUN chmod +x /usr/local/bin/runtime-config.sh

# Create runtime directory
USER root
RUN install -d -m 0775 -o root -g 0 /tmp/runtime-cfg/
ENV RUNTIME_DIR=/tmp/runtime-cfg/
RUN mkdir -p /tmp/runtime-cfg/

# Restore WORKDIR/User to "normal" state
WORKDIR /home/${NB_USER}
USER ${NB_UID}