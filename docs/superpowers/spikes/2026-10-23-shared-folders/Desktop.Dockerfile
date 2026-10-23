ARG BASE=deskyard-debian-xfce:test
FROM ${BASE}
COPY uid-init-patch.py /tmp/uid-init-patch.py
RUN /usr/bin/python3 /tmp/uid-init-patch.py && rm /tmp/uid-init-patch.py
