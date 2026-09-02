// labels is a map of campaign statuses to
// CSS classes
var labels = {
    "In progress": "text-bg-primary",
    "Queued": "text-bg-info",
    "Completed": "text-bg-success",
    "Emails Sent": "text-bg-success",
    "Error": "text-bg-danger"
}

// Campaign selects are native controls. The submitted payload has always
// carried the visible label of the chosen option, so the option text stays the
// source of truth here; only the widget around it changed.
//
// The leading blank option is the placeholder. The previous widget mapped it to
// an empty label, so an untouched control has always submitted "" rather than
// the placeholder wording, and that must stay true.
function selectedOptionText(select) {
    var option = select.options[select.selectedIndex]
    if (!option || option.value === "") {
        return ""
    }
    return option.text
}

// fillSelectOptions rebuilds a single-choice select from an API collection,
// sorted case-insensitively by label as the previous widget sorted its dropdown.
// The placeholder stays selected, which is the state the previous widget
// rendered as placeholder text.
function fillSelectOptions(select, items, placeholder) {
    var sorted = items.slice().sort(function (a, b) {
        return a.name.toLowerCase().localeCompare(b.name.toLowerCase())
    })
    select.innerHTML = ""
    var placeholderOption = document.createElement("option")
    placeholderOption.value = ""
    placeholderOption.text = placeholder
    placeholderOption.disabled = true
    placeholderOption.selected = true
    select.appendChild(placeholderOption)
    sorted.forEach(function (item) {
        var option = document.createElement("option")
        option.value = item.id
        option.text = item.name
        select.appendChild(option)
    })
}

// setSelectPlaceholder renames the placeholder entry, which is how a campaign
// copy reports a template, page, or profile that no longer exists. The
// submitted value stays empty, exactly as before.
function setSelectPlaceholder(select, placeholder) {
    var option = select.options[0]
    if (option && option.value === "") {
        option.text = placeholder
        select.value = ""
    }
}

// groupSelect holds the Tom Select instance backing the multiple-choice groups
// control, the only field that still needs search and removable tags.
var groupSelect = null
var launchRequest = null
var latestCampaignOptionRequest = 0

function setupGroupSelect() {
    if (groupSelect) {
        return groupSelect
    }
    groupSelect = new TomSelect("#users", {
        plugins: ["remove_button"],
        placeholder: "Select Groups",
        maxOptions: null,
        sortField: { field: "text", direction: "asc" },
        render: {
            // Built as a DOM node so the group name, which is user-supplied,
            // can never be interpreted as markup. The title carries the target
            // count the previous widget exposed as an option tooltip.
            option: function (data) {
                var option = document.createElement("div")
                option.textContent = data.text
                if (data.title) {
                    option.title = data.title
                }
                return option
            },
        },
    })
    return groupSelect
}

function selectedGroupNames() {
    var select = document.getElementById("users")
    return Array.prototype.slice.call(select.selectedOptions).map(function (option) {
        return option.text
    })
}

function setCampaignFormDisabled(disabled) {
    ["name", "template", "url", "page", "profile", "launch_date", "send_by_date",
        "users", "launchButton"]
        .forEach(function (id) {
            document.getElementById(id).disabled = disabled
        })
    document.querySelector(
        '#modal button[onclick^="bsModalShow(\'#sendTestEmailModal\'"]'
    ).disabled = disabled
    if (groupSelect) {
        if (disabled) {
            groupSelect.lock()
        } else {
            groupSelect.unlock()
        }
    }
}

var campaigns = []
var campaign = {}

// The escaped dot in the previous jQuery selectors bypassed its
// getElementById fast path, so every element carrying the id was updated.
function clearCampaignFlashes(id) {
    document.querySelectorAll('[id="' + id + '"]').forEach(function (container) {
        container.replaceChildren()
    })
}

function showCampaignFlash(id, variantClass, iconClass, message) {
    document.querySelectorAll('[id="' + id + '"]').forEach(function (container) {
        container.replaceChildren(buildFlash(variantClass, iconClass, message))
    })
}

// :contains() is a jQuery-only selector. It matched every button whose text
// contains "OK", so the native equivalent keeps that cardinality.
function bindOkayButtons(handler) {
    document.querySelectorAll("button").forEach(function (button) {
        if (button.textContent.includes("OK")) {
            button.addEventListener("click", handler)
        }
    })
}

// The launch date fields are native datetime-local controls. The browser holds
// a local wall-clock value formatted as YYYY-MM-DDTHH:mm while the API has
// always received an instant in UTC, so both directions are converted here.
function localDateTimeInputValue(date) {
    var pad = function (value) {
        return (value < 10 ? "0" : "") + value
    }
    return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate()) +
        "T" + pad(date.getHours()) + ":" + pad(date.getMinutes())
}

// utcFromLocalDateTimeInput turns a datetime-local value, which the browser
// expresses in the visitor's own time zone, into the UTC timestamp the API
// expects. An empty or unusable control yields an empty string.
function utcFromLocalDateTimeInput(value) {
    if (!value) {
        return ""
    }
    var parsed = new Date(value)
    if (isNaN(parsed.getTime())) {
        return ""
    }
    return parsed.toISOString().replace(/\.\d{3}Z$/, "Z")
}

// Launch attempts to POST to /campaigns/
function launch() {
    Swal.fire({
        title: "Are you sure?",
        text: "This will schedule the campaign to be launched.",
        type: "question",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Launch",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        showLoaderOnConfirm: true,
        preConfirm: function () {
            if (launchRequest) {
                return launchRequest
            }
            groups = selectedGroupNames().map(function (name) {
                return { name: name }
            })
            // Validate our fields
            var launch_date = utcFromLocalDateTimeInput(document.getElementById("launch_date").value)
            if (!launch_date) {
                // Refuse to schedule rather than post a launch date the API
                // cannot parse, which is what an empty control used to do.
                modalError("Please specify a launch date")
                Swal.close()
                return
            }
            var send_by_date = utcFromLocalDateTimeInput(document.getElementById("send_by_date").value)
            campaign = {
                name: document.getElementById("name").value,
                template: {
                    name: selectedOptionText(document.getElementById("template"))
                },
                url: document.getElementById("url").value,
                page: {
                    name: selectedOptionText(document.getElementById("page"))
                },
                smtp: {
                    name: selectedOptionText(document.getElementById("profile"))
                },
                launch_date: launch_date,
                send_by_date: send_by_date || null,
                groups: groups,
            }
            setCampaignFormDisabled(true)
            launchRequest = api.campaigns.post(campaign)
                .then(function (data) {
                    launchRequest = null
                    setCampaignFormDisabled(false)
                    campaign = data
                }, function (error) {
                    launchRequest = null
                    setCampaignFormDisabled(false)
                    showCampaignFlash(
                        "modal.flashes",
                        "alert-danger",
                        "fa-exclamation-circle",
                        requestErrorMessage(error))
                    Swal.close()
                }
            )
            return launchRequest
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Campaign Scheduled!',
                'This campaign has been scheduled for launch!',
                'success'
            );
        }
        bindOkayButtons(function () {
            window.location = "/campaigns/" + campaign.id.toString()
        })
    })
}

// Attempts to send a test email by POSTing to /campaigns/
function sendTestEmail() {
    var test_email_request = {
        template: {
            name: selectedOptionText(document.getElementById("template"))
        },
        first_name: document.querySelector("input[name=to_first_name]").value,
        last_name: document.querySelector("input[name=to_last_name]").value,
        email: document.querySelector("input[name=to_email]").value,
        position: document.querySelector("input[name=to_position]").value,
        url: document.getElementById("url").value,
        page: {
            name: selectedOptionText(document.getElementById("page"))
        },
        smtp: {
            name: selectedOptionText(document.getElementById("profile"))
        }
    }
    var submit = document.getElementById("sendTestModalSubmit")
    btnHtml = submit.innerHTML
    submit.innerHTML = '<i class="fa fa-spinner fa-spin"></i> Sending'
    // Send the test email
    api.send_test_email(test_email_request)
        .then(function () {
            showCampaignFlash(
                "sendTestEmailModal.flashes",
                "alert-success",
                "fa-check-circle",
                "Email Sent!")
            submit.innerHTML = btnHtml
        }, function (error) {
            showCampaignFlash(
                "sendTestEmailModal.flashes",
                "alert-danger",
                "fa-exclamation-circle",
                requestErrorMessage(error))
            submit.innerHTML = btnHtml
        })
}

function dismiss() {
    latestCampaignOptionRequest += 1
    clearCampaignFlashes("modal.flashes");
    document.getElementById("name").value = "";
    document.getElementById("template").value = "";
    document.getElementById("page").value = "";
    document.getElementById("url").value = "";
    document.getElementById("profile").value = "";
    if (groupSelect) {
        groupSelect.clear(true);
    }
    bsModalHide("#modal");
}

function deleteCampaign(idx) {
    var campaignId = campaigns[idx].id
    var campaignName = campaigns[idx].name
    var deleteRequest = null
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the campaign. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + campaignName,
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            if (deleteRequest) {
                return deleteRequest
            }
            deleteRequest = api.campaignId.delete(campaignId)
                .then(undefined, function (error) {
                    deleteRequest = null
                    Swal.showValidationMessage(requestErrorMessage(error))
                })
            return deleteRequest
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Campaign Deleted!',
                'This campaign has been deleted!',
                'success'
            );
        }
        bindOkayButtons(function () {
            location.reload()
        })
    })
}

function setupOptions() {
    var optionRequest = ++latestCampaignOptionRequest
    setupGroupSelect()
    setCampaignFormDisabled(true)
    var groupsRequest = api.groups.summary()
        .then(function (summaries) {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            groups = summaries.groups
            if (groups.length == 0) {
                modalError("No groups found!")
                return false;
            }
            var select = setupGroupSelect()
            select.clear(true)
            select.clearOptions()
            select.addOptions(groups.map(function (group) {
                return {
                    text: group.name,
                    title: group.num_targets + " targets",
                    value: String(group.id),
                }
            }))
        }, function (error) {
            if (optionRequest == latestCampaignOptionRequest) {
                modalError(requestErrorMessage(error))
            }
        })
    return api.templates.get()
        .then(function (templates) {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            if (templates.length == 0) {
                modalError("No templates found!")
                return false
            }
            var template_select = document.getElementById("template")
            fillSelectOptions(template_select, templates, "Select a Template")
            if (templates.length === 1) {
                template_select.value = templates[0].id
            }
        })
        .then(function () {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            return api.pages.get()
        })
        .then(function (pages) {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            if (pages.length == 0) {
                modalError("No pages found!")
                return false
            }
            var page_select = document.getElementById("page")
            fillSelectOptions(page_select, pages, "Select a Landing Page")
            if (pages.length === 1) {
                page_select.value = pages[0].id
            }
        })
        .then(function () {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            return api.SMTP.get()
        })
        .then(function (profiles) {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            if (profiles.length == 0) {
                modalError("No profiles found!")
                return false
            }
            var profile_select = document.getElementById("profile")
            fillSelectOptions(profile_select, profiles, "Select a Sending Profile")
            // The legacy `select2("val", profile_s2[0])` call stringified an
            // object, matched no option, and therefore left the placeholder
            // selected. Only the single-profile case ever preselected.
            if (profiles.length === 1) {
                profile_select.value = profiles[0].id
            }
        })
        .then(function () {
            return groupsRequest
        })
        .then(function () {
            return optionRequest
        })
}

function edit(campaign) {
    var optionsRequest = setupOptions()
    var optionRequest = latestCampaignOptionRequest
    optionsRequest.then(function () {
        if (optionRequest == latestCampaignOptionRequest) {
            setCampaignFormDisabled(false)
        }
    }, function (error) {
        if (optionRequest == latestCampaignOptionRequest) {
            modalError(requestErrorMessage(error))
        }
    })
}

function copy(idx) {
    var campaignId = campaigns[idx].id
    var optionsRequest = setupOptions()
    var optionRequest = latestCampaignOptionRequest
    optionsRequest
        .then(function () {
            if (optionRequest != latestCampaignOptionRequest) {
                return
            }
            return api.campaignId.get(campaignId)
        })
        .then(function (campaign) {
            if (!campaign || optionRequest != latestCampaignOptionRequest) {
                return
            }
            document.getElementById("name").value = "Copy of " + campaign.name
            var template_select = document.getElementById("template")
            if (!campaign.template.id) {
                setSelectPlaceholder(template_select, campaign.template.name)
            } else {
                template_select.value = campaign.template.id.toString()
            }
            var page_select = document.getElementById("page")
            if (!campaign.page.id) {
                setSelectPlaceholder(page_select, campaign.page.name)
            } else {
                page_select.value = campaign.page.id.toString()
            }
            var profile_select = document.getElementById("profile")
            if (!campaign.smtp.id) {
                setSelectPlaceholder(profile_select, campaign.smtp.name)
            } else {
                profile_select.value = campaign.smtp.id.toString()
            }
            document.getElementById("url").value = campaign.url
            setCampaignFormDisabled(false)
        }, function (error) {
            if (optionRequest == latestCampaignOptionRequest) {
                showCampaignFlash(
                    "modal.flashes",
                    "alert-danger",
                    "fa-exclamation-circle",
                    requestErrorMessage(error))
            }
        })
}

document.addEventListener("DOMContentLoaded", function () {
    // Prefill the launch date with the current local time, as the previous
    // picker did; the optional "send emails by" field stays empty.
    document.getElementById("launch_date").value = localDateTimeInputValue(new Date())
    // Modal dismiss handler (native Bootstrap 5 event, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    api.campaigns.summary()
        .then(function (data) {
            campaigns = data.campaigns
            document.getElementById("loading").style.display = "none"
            if (campaigns.length > 0) {
                document.getElementById("campaignTable").style.display = ""
                document.getElementById("campaignTableArchive").style.display = ""

                activeCampaignsTable = new DataTable("#campaignTable", {
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }],
                    order: [
                        [1, "desc"]
                    ]
                });
                archivedCampaignsTable = new DataTable("#campaignTableArchive", {
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }],
                    order: [
                        [1, "desc"]
                    ]
                });
                rows = {
                    'active': [],
                    'archived': []
                }
                campaigns.forEach(function (campaign, i) {
                    label = labels[campaign.status] || "text-bg-secondary";

                    //section for tooltips on the status of a campaign to show some quick stats
                    var launchDate;
                    if (moment(campaign.launch_date).isAfter(moment())) {
                        launchDate = "Scheduled to start: " + moment(campaign.launch_date).format('MMMM Do YYYY, h:mm:ss a')
                        var quickStats = launchDate + "<br><br>" + "Number of recipients: " + campaign.stats.total
                    } else {
                        launchDate = "Launch Date: " + moment(campaign.launch_date).format('MMMM Do YYYY, h:mm:ss a')
                        var quickStats = launchDate + "<br><br>" + "Number of recipients: " + campaign.stats.total + "<br><br>" + "Emails opened: " + campaign.stats.opened + "<br><br>" + "Emails clicked: " + campaign.stats.clicked + "<br><br>" + "Submitted Credentials: " + campaign.stats.submitted_data + "<br><br>" + "Errors : " + campaign.stats.error + "<br><br>" + "Reported : " + campaign.stats.email_reported
                    }

                    var row = [
                        escapeHtml(campaign.name),
                        moment(campaign.created_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<span class=\"badge " + label + "\" data-bs-toggle=\"tooltip\" data-bs-placement=\"right\" data-bs-html=\"true\" title=\"" + quickStats + "\">" + campaign.status + "</span>",
                        "<div class='float-end'><a class='btn btn-primary' href='/campaigns/" + campaign.id + "' data-bs-toggle='tooltip' data-bs-placement='left' title='View Results'>\
                    <i class='fa fa-bar-chart'></i>\
                    </a>\
            <span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Copy Campaign' onclick='copy(" + i + ")'>\
                    <i class='fa fa-copy'></i>\
                    </button></span>\
                    <button class='btn btn-danger' onclick='deleteCampaign(" + i + ")' data-bs-toggle='tooltip' data-bs-placement='left' title='Delete Campaign'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ]
                    if (campaign.status == 'Completed') {
                        rows['archived'].push(row)
                    } else {
                        rows['active'].push(row)
                    }
                })
                activeCampaignsTable.rows.add(rows['active']).draw()
                archivedCampaignsTable.rows.add(rows['archived']).draw()
                bsInitTooltips()
            } else {
                // The page contains two elements with this id. A plain jQuery
                // id selector used its getElementById fast path, so only the
                // first empty-state block was shown.
                document.getElementById("emptyMessage").style.display = ""
            }
        }, function () {
            document.getElementById("loading").style.display = "none"
            errorFlash("Error fetching campaigns")
        })
})
