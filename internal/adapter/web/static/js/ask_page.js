// Alpine 组件：AI 问数（/ask）。单轮问答，POST /api/ask。
function askPage() {
  return {
    question: '',
    askedQuestion: '',
    answer: null,
    loading: false,
    error: '',

    askPreset(q) {
      this.question = q;
      this.submit();
    },

    async submit() {
      const q = this.question.trim();
      if (!q || this.loading) return;
      this.loading = true;
      this.error = '';
      this.answer = null;
      this.askedQuestion = q;
      try {
        const r = await fetch('/api/ask', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ question: q }),
        });
        if (!r.ok) throw new Error(await r.text());
        this.answer = await r.json();
      } catch (e) {
        this.error = e.message;
      } finally {
        this.loading = false;
      }
    },
  };
}
