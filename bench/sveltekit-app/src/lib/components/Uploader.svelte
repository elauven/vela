<script>
  let { issueId = 0, onuploaded } = $props();

  let progress = $state(-1);
  let error = $state('');

  function upload(event) {
    const input = event.target;
    const file = input.files[0];
    if (!file) return;
    const form = new FormData();
    form.append('file', file);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/issues/${issueId}/attachments`);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) progress = Math.round((e.loaded / e.total) * 100);
    };
    xhr.onload = () => {
      progress = -1;
      input.value = '';
      if (xhr.status === 201) {
        error = '';
        onuploaded?.(JSON.parse(xhr.responseText));
      } else {
        error = `Upload failed: HTTP ${xhr.status}`;
      }
    };
    xhr.onerror = () => {
      progress = -1;
      error = 'Upload failed';
    };
    progress = 0;
    xhr.send(form);
  }
</script>

<div>
  <input type="file" onchange={upload} disabled={progress >= 0} />
  {#if progress >= 0}
    <div class="progress"><div style:width={`${progress}%`}></div></div>
  {/if}
  {#if error}
    <p class="error">{error}</p>
  {/if}
</div>
