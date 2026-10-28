document.getElementById('run').onclick = async () => {
  const q = document.getElementById('query').value;
  const res = await fetch('/ask', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({query: q, backend: 'rag_research'})
  });
  const data = await res.json();
  document.getElementById('zone').textContent = 'Zone: ' + (data.calibrated_decision || data.decision || '');
  const claims = data.span_level_attribution || [];
  const container = document.getElementById('claims');
  container.innerHTML = '';
  claims.forEach(c => {
    const div = document.createElement('div');
    div.className = 'claim';
    const score = c.score || 0;
    if (score >= 0.6) div.classList.add('green');
    else if (score >= 0.3) div.classList.add('yellow');
    else div.classList.add('red');
    div.textContent = (c.claim_span || '') + ' -> ' + (c.text || '') + ' (' + score + ')';
    container.appendChild(div);
  });
  const exportBtn = document.createElement('button');
  exportBtn.textContent = 'Export JSON';
  exportBtn.onclick = () => {
    const blob = new Blob([JSON.stringify(data, null, 2)], {type:'application/json'});
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = 'dossier.json'; a.click();
  };
  document.getElementById('export').innerHTML = '';
  document.getElementById('export').appendChild(exportBtn);
};
