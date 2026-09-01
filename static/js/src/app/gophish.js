/*
 * Bootstrap 5 helpers.
 *
 * These call Bootstrap's native JS API (window.bootstrap, exposed by
 * bootstrap.bundle.min.js) instead of the optional jQuery plugin bridge, so
 * that Bootstrap is invoked directly rather than through jQuery.
 */
function bsModalHide(selector) {
    var el = typeof selector === "string" ? document.querySelector(selector) : selector;
    if (!el) return;
    var modal = bootstrap.Modal.getInstance(el);
    if (modal) modal.hide();
}
window.bsModalHide = bsModalHide;

function bsInitTooltips(root) {
    (root || document).querySelectorAll('[data-bs-toggle="tooltip"]').forEach(function (el) {
        if (el.getClientRects().length === 0) {
            return;
        }
        if (!bootstrap.Tooltip.getInstance(el)) {
            new bootstrap.Tooltip(el, { animation: false });
        }
    });
}
window.bsInitTooltips = bsInitTooltips;

function bsHideTooltips(root) {
    (root || document).querySelectorAll('[data-bs-toggle="tooltip"]').forEach(function (el) {
        var tooltip = bootstrap.Tooltip.getInstance(el);
        if (tooltip) {
            tooltip.hide();
            tooltip.dispose();
        }
    });
}

// bsHideTooltip hides a visible-element's tooltip before the element is hidden
// with something like jQuery's .hide(). Bootstrap 5's
// Tooltip.show() throws if its trigger has inline style="display: none",
// which can happen when a pending hover-triggered show fires after the
// trigger element itself is hidden.
function bsHideTooltip(selector) {
    var el = typeof selector === "string" ? document.querySelector(selector) : selector;
    if (!el) return;
    var tooltip = bootstrap.Tooltip.getInstance(el);
    if (tooltip) {
        tooltip.hide();
        tooltip.dispose();
    }
}
window.bsHideTooltip = bsHideTooltip;


function bsModalShow(selector, trigger) {
    var target = typeof selector === "string" ? document.querySelector(selector) : selector;
    if (!target) return;
    var triggerEl = trigger && trigger.nodeType === 1 ? trigger : null;
    if (triggerEl && !target.__fvRestoreFocusHandler) {
        target.__fvRestoreFocusHandler = function () {
            if (target.__fvModalTrigger && typeof target.__fvModalTrigger.focus === "function") {
                target.__fvModalTrigger.focus();
            }
            target.__fvModalTrigger = null;
        };
        target.addEventListener("hidden.bs.modal", target.__fvRestoreFocusHandler);
    }
    if (triggerEl) {
        target.__fvModalTrigger = triggerEl;
    }
    bootstrap.Modal.getOrCreateInstance(target, { backdrop: "static", keyboard: false }).show();
}
window.bsModalShow = bsModalShow;

// buildFlash renders one dismissable message. The message is inserted as text
// rather than parsed as markup: these strings come from API responses, and the
// previous implementation appended them as HTML, which made any markup in a
// server message render as markup. Nothing in the application relies on that.
function buildFlash(variantClass, iconClass, message) {
    var alert = document.createElement("div")
    alert.style.textAlign = "center"
    alert.className = "alert " + variantClass
    var icon = document.createElement("i")
    icon.className = "fa " + iconClass
    alert.append(icon, " " + message)
    return alert
}

// showFlash replaces the contents of the page-level message area. There can be
// more than one element carrying the "flashes" id on a page, and only the first
// has ever been written to, which getElementById preserves.
function showFlash(variantClass, iconClass, message) {
    var container = document.getElementById("flashes")
    if (!container) {
        return null
    }
    container.replaceChildren(buildFlash(variantClass, iconClass, message))
    return container
}

function errorFlash(message) {
    showFlash("alert-danger", "fa-exclamation-circle", message)
}

function successFlash(message) {
    showFlash("alert-success", "fa-check-circle", message)
}

// Fade message after n seconds
function errorFlashFade(message, fade) {
    var container = showFlash("alert-danger", "fa-exclamation-circle", message)
    if (!container) {
        return
    }
    setTimeout(function () {
        container.replaceChildren()
    }, fade * 1000);
}
// Fade message after n seconds
function successFlashFade(message, fade) {
    var container = showFlash("alert-success", "fa-check-circle", message)
    if (!container) {
        return
    }
    setTimeout(function () {
        container.replaceChildren()
    }, fade * 1000);
}

function modalError(message) {
    // Unlike "#flashes", the escaped-dot selector this replaces did not resolve
    // through getElementById: it matched every element carrying the id. Two
    // pages carry two of them, one in the page modal and one in a stacked
    // import modal, so writing to only the first would hide import errors
    // behind the modal on top.
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren(
            buildFlash("alert-danger", "fa-exclamation-circle", message)
        )
    })
}

class APIRequestError extends Error {
    constructor(message, options) {
        super(message)
        this.name = "APIRequestError"
        this.status = options.status
        this.statusText = options.statusText
        this.data = options.data
        this.responseText = options.responseText
        this.cause = options.cause
    }
}

function requestJSON(endpoint, method, data) {
    var serializedData = JSON.stringify(data)
    var requestMethod = method.toUpperCase()
    var url = "/api" + endpoint
    var options = {
        method: requestMethod,
        credentials: "same-origin",
        headers: {
            "Accept": "application/json, text/javascript, */*; q=0.01",
            "Authorization": "Bearer " + user.api_key,
            "Content-Type": "application/json"
        }
    }

    if (requestMethod == "GET" || requestMethod == "HEAD") {
        if (serializedData !== undefined) {
            url += (url.includes("?") ? "&" : "?") + serializedData
        }
    } else if (serializedData !== undefined) {
        options.body = serializedData
    }

    return fetch(url, options)
        .then(function (response) {
            return response.text()
                .then(function (responseText) {
                    var responseData
                    if (responseText) {
                        try {
                            responseData = JSON.parse(responseText)
                        } catch (cause) {
                            throw new APIRequestError("Invalid JSON response", {
                                status: response.status,
                                statusText: "parsererror",
                                responseText: responseText,
                                cause: cause
                            })
                        }
                    } else if (response.status != 204) {
                        throw new APIRequestError("Invalid JSON response", {
                            status: response.status,
                            statusText: "parsererror",
                            responseText: responseText
                        })
                    }

                    if (!response.ok) {
                        throw new APIRequestError(response.statusText || "HTTP request failed", {
                            status: response.status,
                            statusText: response.statusText,
                            data: responseData,
                            responseText: responseText
                        })
                    }
                    return responseData
                })
        })
        .catch(function (error) {
            if (error instanceof APIRequestError) {
                throw error
            }
            throw new APIRequestError("Network request failed", {
                status: 0,
                statusText: "error",
                responseText: "",
                cause: error
            })
        })
}
window.requestJSON = requestJSON

function requestErrorMessage(error) {
    if (error && error.data && typeof error.data.message == "string") {
        return error.data.message
    }
    return "Request failed"
}

function query(endpoint, method, data, async) {
    return $.ajax({
        url: "/api" + endpoint,
        async: async,
        method: method,
        data: JSON.stringify(data),
        dataType: "json",
        contentType: "application/json",
        beforeSend: function (xhr) {
            xhr.setRequestHeader('Authorization', 'Bearer ' + user.api_key);
        }
    })
}

function escapeHtml(text) {
    var element = document.createElement("div")
    element.textContent = text
    return element.innerHTML
}
window.escapeHtml = escapeHtml

function unescapeHtml(html) {
    // The previous implementation treated undefined as "read", not "write", and
    // so returned an empty string. Assigning it to innerHTML would instead
    // produce the literal text "undefined".
    if (html === undefined) {
        return ""
    }
    var element = document.createElement("div")
    element.innerHTML = html
    return element.textContent
}

/**
 *
 * @param {string} string - The input string to capitalize
 *
 */
var capitalize = function (string) {
    return string.charAt(0).toUpperCase() + string.slice(1);
}

/*
Define our API Endpoints
*/
var api = {
    // campaigns contains the endpoints for /campaigns
    campaigns: {
        // get() - Queries the API for GET /campaigns
        get: function () {
            return query("/campaigns/", "GET", {}, false)
        },
        // post() - Posts a campaign to POST /campaigns
        post: function (data) {
            return query("/campaigns/", "POST", data, false)
        },
        // summary() - Queries the API for GET /campaigns/summary
        summary: function () {
            return query("/campaigns/summary", "GET", {}, false)
        }
    },
    // campaignId contains the endpoints for /campaigns/:id
    campaignId: {
        // get() - Queries the API for GET /campaigns/:id
        get: function (id) {
            return requestJSON("/campaigns/" + id, "GET", {})
        },
        // delete() - Deletes a campaign at DELETE /campaigns/:id
        delete: function (id) {
            return query("/campaigns/" + id, "DELETE", {}, false)
        },
        // results() - Queries the API for GET /campaigns/:id/results
        results: function (id) {
            return requestJSON("/campaigns/" + id + "/results", "GET", {})
        },
        // complete() - Completes a campaign at POST /campaigns/:id/complete
        complete: function (id) {
            return requestJSON("/campaigns/" + id + "/complete", "GET", {})
        },
        // summary() - Queries the API for GET /campaigns/summary
        summary: function (id) {
            return query("/campaigns/" + id + "/summary", "GET", {}, true)
        }
    },
    // groups contains the endpoints for /groups
    groups: {
        // get() - Queries the API for GET /groups
        get: function () {
            return query("/groups/", "GET", {}, false)
        },
        // post() - Posts a group to POST /groups
        post: function (group) {
            return query("/groups/", "POST", group, false)
        },
        // summary() - Queries the API for GET /groups/summary
        summary: function () {
            return requestJSON("/groups/summary", "GET", {})
        }
    },
    // groupId contains the endpoints for /groups/:id
    groupId: {
        // get() - Queries the API for GET /groups/:id
        get: function (id) {
            return query("/groups/" + id, "GET", {}, false)
        },
        // put() - Puts a group to PUT /groups/:id
        put: function (group) {
            return query("/groups/" + group.id, "PUT", group, false)
        },
        // delete() - Deletes a group at DELETE /groups/:id
        delete: function (id) {
            return query("/groups/" + id, "DELETE", {}, false)
        }
    },
    // templates contains the endpoints for /templates
    templates: {
        // get() - Queries the API for GET /templates
        get: function () {
            return query("/templates/", "GET", {}, false)
        },
        // post() - Posts a template to POST /templates
        post: function (template) {
            return query("/templates/", "POST", template, false)
        }
    },
    // templateId contains the endpoints for /templates/:id
    templateId: {
        // get() - Queries the API for GET /templates/:id
        get: function (id) {
            return query("/templates/" + id, "GET", {}, false)
        },
        // put() - Puts a template to PUT /templates/:id
        put: function (template) {
            return query("/templates/" + template.id, "PUT", template, false)
        },
        // delete() - Deletes a template at DELETE /templates/:id
        delete: function (id) {
            return query("/templates/" + id, "DELETE", {}, false)
        }
    },
    // pages contains the endpoints for /pages
    pages: {
        // get() - Queries the API for GET /pages
        get: function () {
            return query("/pages/", "GET", {}, false)
        },
        // post() - Posts a page to POST /pages
        post: function (page) {
            return query("/pages/", "POST", page, false)
        }
    },
    // pageId contains the endpoints for /pages/:id
    pageId: {
        // get() - Queries the API for GET /pages/:id
        get: function (id) {
            return query("/pages/" + id, "GET", {}, false)
        },
        // put() - Puts a page to PUT /pages/:id
        put: function (page) {
            return query("/pages/" + page.id, "PUT", page, false)
        },
        // delete() - Deletes a page at DELETE /pages/:id
        delete: function (id) {
            return query("/pages/" + id, "DELETE", {}, false)
        }
    },
    // SMTP contains the endpoints for /smtp
    SMTP: {
        // get() - Queries the API for GET /smtp
        get: function () {
            return query("/smtp/", "GET", {}, false)
        },
        // post() - Posts a SMTP to POST /smtp
        post: function (smtp) {
            return query("/smtp/", "POST", smtp, false)
        }
    },
    // SMTPId contains the endpoints for /smtp/:id
    SMTPId: {
        // get() - Queries the API for GET /smtp/:id
        get: function (id) {
            return query("/smtp/" + id, "GET", {}, false)
        },
        // put() - Puts a SMTP to PUT /smtp/:id
        put: function (smtp) {
            return query("/smtp/" + smtp.id, "PUT", smtp, false)
        },
        // delete() - Deletes a SMTP at DELETE /smtp/:id
        delete: function (id) {
            return query("/smtp/" + id, "DELETE", {}, false)
        }
    },
    // IMAP containts the endpoints for /imap/
    IMAP: {
        get: function() {
            return query("/imap/", "GET", {}, !1)
        },
        post: function(e) {
            return query("/imap/", "POST", e, !1)
        },
        validate: function(e) {
            return requestJSON("/imap/validate", "POST", e)
        }
    },
    // users contains the endpoints for /users
    users: {
        // get() - Queries the API for GET /users
        get: function () {
            return requestJSON("/users/", "GET", {})
        },
        // post() - Posts a user to POST /users
        post: function (user) {
            return requestJSON("/users/", "POST", user)
        }
    },
    // userId contains the endpoints for /users/:id
    userId: {
        // get() - Queries the API for GET /users/:id
        get: function (id) {
            return requestJSON("/users/" + id, "GET", {})
        },
        // put() - Puts a user to PUT /users/:id
        put: function (user) {
            return requestJSON("/users/" + user.id, "PUT", user)
        },
        // delete() - Deletes a user at DELETE /users/:id
        delete: function (id) {
            return requestJSON("/users/" + id, "DELETE", {})
        }
    },
    webhooks: {
        get: function() {
            return query("/webhooks/", "GET", {}, false)
        },
        post: function(webhook) {
            return query("/webhooks/", "POST", webhook, false)
        },
    },
    webhookId: {
        get: function(id) {
            return query("/webhooks/" + id, "GET", {}, false)
        },
        put: function(webhook) {
            return requestJSON("/webhooks/" + webhook.id, "PUT", webhook)
        },
        delete: function(id) {
            return query("/webhooks/" + id, "DELETE", {}, false)
        },
        ping: function(id) {
            return requestJSON("/webhooks/" + id + "/validate", "POST", {})
        },
    },
    // import handles all of the "import" functions in the api
    import_email: function (req) {
        return query("/import/email", "POST", req, false)
    },
    // clone_site handles importing a site by url
    clone_site: function (req) {
        return query("/import/site", "POST", req, false)
    },
    // send_test_email sends an email to the specified email address
    send_test_email: function (req) {
        return requestJSON("/util/send_test_email", "POST", req)
    },
    reset: function () {
        return requestJSON("/reset", "POST", {})
    }
}
window.api = api

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    // Setup nav highlighting
    var path = location.pathname;
    document.querySelectorAll('.nav-sidebar li').forEach(function (item) {
        // if the current path is like this link, make it active
        var link = item.querySelector('a');
        if (link && link.getAttribute('href') === path) {
            item.classList.add('active');
        }
    })
    // Registers the date format the tables render, so their date columns are
    // ordered chronologically rather than as text. This replaces the deprecated
    // datetime-moment plug-in and needs no jQuery: DataTables picks up the
    // Moment.js global loaded before it.
    var dateFormat = 'MMMM Do YYYY, h:mm:ss a';
    DataTable.datetime(dateFormat);
    // Registering a date type also right-aligns the columns it matches. These
    // tables have always shown their dates left-aligned, so the automatic class
    // is cleared rather than silently restyling every date column.
    DataTable.type('datetime-' + dateFormat, 'className', '');
    // DataTables 3 cycles a column through ascending, descending and then no
    // ordering at all. Every table here has always cycled between ascending and
    // descending only, so the two-state sequence is kept explicitly rather than
    // letting the upgrade change how sorting behaves.
    DataTable.defaults.column.orderSequence = ['asc', 'desc'];
    // Setup tooltips
    bsInitTooltips()

    // Centralized Bootstrap 5 modal-stack management (native events, no jQuery bridge).
    // Handles z-index stacking for multiple simultaneous modals and the scrollbar fix.
    // BS5 base: modal 1055, backdrop 1050; each additional layer adds 10.
    document.addEventListener('hide.bs.modal', function (event) {
        bsHideTooltips(event.target);
    });
    document.addEventListener('hidden.bs.modal', function (event) {
        var modal = event.target;
        if (!modal.classList.contains('modal')) return;
        modal.classList.remove('fv-modal-stack');
        modal.style.zIndex = '';
        var count = parseInt(document.body.getAttribute('data-fv-open-modals') || '0', 10);
        count = Math.max(0, count - 1);
        document.body.setAttribute('data-fv-open-modals', String(count));
        if (document.querySelector('.modal.show')) {
            document.body.classList.add('modal-open');
        }
    });
    document.addEventListener('shown.bs.modal', function (event) {
        var modal = event.target;
        if (!modal.classList.contains('modal')) return;
        bsInitTooltips(modal);
        if (modal.classList.contains('fv-modal-stack')) return;
        modal.classList.add('fv-modal-stack');
        var count = parseInt(document.body.getAttribute('data-fv-open-modals') || '0', 10);
        count++;
        document.body.setAttribute('data-fv-open-modals', String(count));
        modal.style.zIndex = String(1045 + (10 * count));
        var backdrops = document.querySelectorAll('.modal-backdrop:not(.fv-modal-stack)');
        backdrops.forEach(function (backdrop) {
            backdrop.style.zIndex = String(1040 + (10 * count));
            backdrop.classList.add('fv-modal-stack');
        });
    });
});