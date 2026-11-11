# Local test seeder (compose profile "test"): a generated clip, a tiny tracker and a one-item Torznab feed.
# No external content: everything is produced at build time.
FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4 AS media
RUN apk add --no-cache ffmpeg mktorrent
WORKDIR /media
ARG CLIP_SECONDS=120
RUN ffmpeg -hide_banner -loglevel error \
      -f lavfi -i testsrc2=size=1280x720:rate=25 -f lavfi -i sine=frequency=440:sample_rate=48000 \
      -t "$CLIP_SECONDS" -c:v libx264 -preset veryfast -pix_fmt yuv420p -c:a aac -b:a 128k -movflags +faststart \
      "Media Stack Test Clip (2026).mp4" \
 && mktorrent -a http://seeder:9117/announce -o /media/test.torrent "Media Stack Test Clip (2026).mp4"

FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4
RUN apk add --no-cache aria2 python3
COPY --from=media /media /media
COPY seeder.py /seeder.py
EXPOSE 9117 6881
CMD ["python3", "-u", "/seeder.py"]
