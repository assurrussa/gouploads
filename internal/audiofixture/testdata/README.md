# Synthetic audio fixtures

These files contain only a generated 440 Hz sine tone (0.05 seconds, mono,
44.1 kHz), with no user recordings or personal metadata. Generated with FFmpeg:

```sh
ffmpeg -f lavfi -i 'sine=frequency=440:duration=0.05:sample_rate=44100' \
  -ac 1 -c:a libmp3lame -b:a 128k -map_metadata -1 -id3v2_version 3 tone.mp3
ffmpeg -f lavfi -i 'sine=frequency=440:duration=0.05:sample_rate=44100' \
  -ac 1 -c:a pcm_s16le -map_metadata -1 tone.wav
```

Tests derive an untagged MP3 by removing the sync-safe ID3 header and tag.
