import { getAuthUser } from '../auth.js';
import { doGet, doPost, subscribe } from '../http.js';
import renderPost from './post.js';

const PAGE_SIZE = 10

const template = document.createElement('template')
template.innerHTML = `
    <div class="container">
        <h1>Timeline</h1>
        <form id="post-form" class="post-form">
            <textarea placeholder="Write something..." maxlength="480" required></textarea>
            <button class="post-form-button" hidden>Publish</button>
        </form>
        <button id="flush-queue-button"
            class="flush-posts-queue"
            aria-live="assertive"
            aria-atomic="true"
            hidden></button>
        <div id="timeline-feed" role="feed"></div>
        <button id="load-more-button" class="load-more-posts-button" hidden>Load more</button>
    </div>
`

export default async function renderHomePage() {
    const timelineQueue = /** @type {import('../types.js').TimelineItem[]} */ ([])
    const timeline = await http.timeline()

    const page = /** @type {DocumentFragment} */ (template.content.cloneNode(true))
    const postForm = /** @type {HTMLFormElement} */ (page.getElementById('post-form'))
    const postFormTextArea = postForm.querySelector('textarea')
    const postFormButton = postForm.querySelector('button')
    const flushQueueButton = /** @type {HTMLButtonElement} */ (page.getElementById('flush-queue-button'))
    const timelineFeed = /** @type {HTMLDivElement} */ (page.getElementById('timeline-feed'))
    const loadMoreButton = /** @type {HTMLButtonElement} */ (page.getElementById('load-more-button'))

    /**
     * @param {Event} ev
     */
    const onPostFormSubmit = async ev => {
        ev.preventDefault()
        const content = postFormTextArea.value

        postFormTextArea.disabled = true
        postFormButton.disabled = true

        try {
            const timelineItem = await http.publishPost({ content })

            flushQueue()

            timeline.unshift(timelineItem)
            timelineFeed.insertAdjacentElement('afterbegin', renderTimelineItem(timelineItem))

            postForm.reset()
            postFormButton.hidden = true
        } catch (err) {
            console.error(err)
            alert(err.message)
            setTimeout(() => {
                postFormTextArea.focus()
            })
        } finally {
            postFormTextArea.disabled = false
            postFormButton.disabled = false
        }
    }

    const onPostFormTextAreaInput = () => {
        postFormButton.hidden = postFormTextArea.value === ''
    }

    const flushQueue = () => {
        let timelineItem = timelineQueue.pop()

        while (timelineItem !== undefined) {
            timeline.unshift(timelineItem)
            timelineFeed.insertAdjacentElement('afterbegin', renderTimelineItem(timelineItem))

            timelineItem = timelineQueue.pop()
        }

        flushQueueButton.hidden = true
    }

    const onFlushQueueButtonClick = flushQueue

    const onLoadMoreButtonClick = async () => {
        loadMoreButton.disabled = true
        timelineFeed.setAttribute('aria-busy', 'true')

        try {
            const lastTimelineItem = timeline[timeline.length - 1]
            const newTimelineItems = await http.timeline(lastTimelineItem.id)

            timeline.push(...newTimelineItems)
            for (const timelineItem of newTimelineItems) {
                timelineFeed.appendChild(renderTimelineItem(timelineItem))
            }

            if (newTimelineItems.length < PAGE_SIZE) {
                loadMoreButton.removeEventListener('click', onLoadMoreButtonClick)
                loadMoreButton.remove()
            }
        } catch (err) {
            console.error(err)
            alert(err.message)
        } finally {
            loadMoreButton.disabled = false
            timelineFeed.setAttribute('aria-busy', 'false')
        }
    }

    /**
     * @param {import('../types.js').TimelineItem} timelineItem
     */
    const onTimelineItemArrive = timelineItem => {
        timelineQueue.unshift(timelineItem)

        flushQueueButton.textContent = timelineQueue.length + ' new posts'
        flushQueueButton.hidden = false
    }

    const unsubscribeFromTimeline = http.subscribeToTimeline(onTimelineItemArrive)

    const onPageDisconnect = unsubscribeFromTimeline

    for (const timelineItem of timeline) {
        timelineFeed.appendChild(renderTimelineItem(timelineItem))
    }

    postForm.addEventListener('submit', onPostFormSubmit)
    postFormTextArea.addEventListener('input', onPostFormTextAreaInput)
    flushQueueButton.addEventListener('click', onFlushQueueButtonClick)
    if (timeline.length == PAGE_SIZE) {
        loadMoreButton.hidden = false
        loadMoreButton.addEventListener('click', onLoadMoreButtonClick)
    }
    page.addEventListener('disconnect', onPageDisconnect)

    return page
}

/**
 * @param {import('../types.js').TimelineItem} timelineItem
 */
function renderTimelineItem(timelineItem) {
    return renderPost(timelineItem.post, timelineItem.id, true)
}

const http = {
    /**
     * @param {import('../types.js').CreatePostInput} input
     * @returns {Promise<import('../types.js').TimelineItem>}
     */
    publishPost: input => doPost('/api/posts', input).then(timelineItem => {
        timelineItem.post.user = getAuthUser()
        return timelineItem
    }),

    /**
     * @param {bigint=} before
     * @returns {Promise<import('../types.js').TimelineItem[]>}
     */
    timeline: (before = 0n) => doGet(`/api/timeline?before=${before}&last=${PAGE_SIZE}`),

    /**
     * @param {function(import('../types.js').TimelineItem): any} cb
     */
    subscribeToTimeline: cb => subscribe('/api/timeline', cb),
}
