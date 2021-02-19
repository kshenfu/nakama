package handler

import (
	"mime"
	"net/http"
	"strconv"

	"github.com/matryer/way"
	"github.com/nicolasparada/nakama/internal/service"
)

func (h *handler) timeline(w http.ResponseWriter, r *http.Request) {
	if a, _, err := mime.ParseMediaType(r.Header.Get("Accept")); err == nil && a == "text/event-stream" {
		h.timelineItemStream(w, r)
		return
	}

	ctx := r.Context()
	q := r.URL.Query()
	last, _ := strconv.Atoi(q.Get("last"))
	before := q.Get("before")
	tt, err := h.Timeline(ctx, last, before)
	if err == service.ErrUnauthenticated {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	if err != nil {
		respondErr(w, err)
		return
	}
	//虽然可以很快获得 http.Response 对象，但它并不表示最终任务完成（请求结束），而是先返回请求状态码，再不断返回拉取进度(通过http.Flusher)，那怎样才知道任务完成了呢？
	respond(w, tt, http.StatusOK)
}

func (h *handler) timelineItemStream(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		respondErr(w, errStreamingUnsupported)
		return
	}

	tt, err := h.TimelineItemStream(r.Context())
	if err == service.ErrUnauthenticated {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	if err != nil {
		respondErr(w, err)
		return
	}

	header := w.Header()
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("Content-Type", "text/event-stream; charset=utf-8")

	// 这个循环如何终止呢？？ h.TimelineItemStream() 中有一个协程调用了 close(tt)，遍历就会终止了
	for ti := range tt {
		writeSSE(w, ti)
		f.Flush()
	}
}

func (h *handler) deleteTimelineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	timelineItemID := way.Param(ctx, "timeline_item_id")
	err := h.DeleteTimelineItem(ctx, timelineItemID)
	if err == service.ErrUnauthenticated {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	if err == service.ErrInvalidTimelineItemID {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	if err != nil {
		respondErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
