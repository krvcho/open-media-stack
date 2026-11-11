FROM prom/alertmanager:v0.34.1@sha256:e9733bafb1bdef9b00e25a21f8f99dc26a22224bf16641ad754d1649f4c3357a
COPY alertmanager.yml telegram.tmpl entrypoint.sh /etc/alertmanager/
ENTRYPOINT ["/bin/sh", "/etc/alertmanager/entrypoint.sh"]
CMD ["--config.file=/tmp/alertmanager.yml", "--storage.path=/alertmanager"]
